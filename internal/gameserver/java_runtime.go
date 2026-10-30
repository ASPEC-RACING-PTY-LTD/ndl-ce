package gameserver

import (
	"archive/zip"
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// javaOfJar is the Java release a server jar's main class was compiled
// for, or 0 when it cannot tell. Mojang moves to a newer Java with some
// releases (26.1 needs Java 25), and "latest" picks those up on install.
func javaOfJar(path string) int {
	if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
		return 0
	}
	zr, err := zip.OpenReader(path)
	if err != nil {
		return 0
	}
	defer zr.Close()
	main := ""
	if f, err := zr.Open("META-INF/MANIFEST.MF"); err == nil {
		sc := bufio.NewScanner(io.LimitReader(f, 64<<10))
		for sc.Scan() {
			if v, ok := strings.CutPrefix(sc.Text(), "Main-Class:"); ok {
				main = strings.TrimSpace(v)
				break
			}
		}
		f.Close()
	}
	if main == "" {
		return 0
	}
	f, err := zr.Open(strings.ReplaceAll(main, ".", "/") + ".class")
	if err != nil {
		return 0
	}
	defer f.Close()
	var head [8]byte
	if _, err := io.ReadFull(f, head[:]); err != nil || binary.BigEndian.Uint32(head[:4]) != 0xCAFEBABE {
		return 0
	}
	// Class file major version 52 is Java 8, 65 is Java 21, 69 is Java 25.
	major := int(binary.BigEndian.Uint16(head[6:8]))
	if major < 52 {
		return 0
	}
	return major - 44
}

// javaImageFor keeps image unless it is an eclipse-temurin runtime older
// than the server jar needs, in which case it returns the matching one.
func javaImageFor(image, dir string, env map[string]string) (string, string) {
	have := javaOfImage(image)
	if have == 0 {
		return image, ""
	}
	jar := filepath.Join(dir, filepath.Clean("/"+firstNonEmpty(env["SERVER_JARFILE"], "server.jar")))
	need := javaOfJar(jar)
	if need <= have {
		return image, ""
	}
	next := fmt.Sprintf("eclipse-temurin:%d-jre", need)
	return next, fmt.Sprintf("%s needs Java %d; the selected runtime is Java %d, so this start uses %s", filepath.Base(jar), need, have, next)
}
