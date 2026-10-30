package gameserver

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestJar(t *testing.T, path string, major byte) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	m, _ := zw.Create("META-INF/MANIFEST.MF")
	_, _ = m.Write([]byte("Manifest-Version: 1.0\r\nMain-Class: net.minecraft.bundler.Main\r\n"))
	c, _ := zw.Create("net/minecraft/bundler/Main.class")
	_, _ = c.Write([]byte{0xCA, 0xFE, 0xBA, 0xBE, 0, 0, 0, major})
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
}

func TestJavaImageForUpgradesOldRuntime(t *testing.T) {
	dir := t.TempDir()
	writeTestJar(t, filepath.Join(dir, "server.jar"), 69)
	if got := javaOfJar(filepath.Join(dir, "server.jar")); got != 25 {
		t.Fatalf("javaOfJar = %d, want 25", got)
	}
	image, note := javaImageFor("eclipse-temurin:21-jre", dir, nil)
	if image != "eclipse-temurin:25-jre" || !strings.Contains(note, "Java 25") {
		t.Fatalf("got %q %q", image, note)
	}
	// A new enough runtime, or a non-Java image, is kept.
	for _, keep := range []string{"eclipse-temurin:25-jre", "debian:bookworm-slim"} {
		if image, _ := javaImageFor(keep, dir, nil); image != keep {
			t.Fatalf("%s changed to %s", keep, image)
		}
	}
	// A missing jar, or one outside the server folder, changes nothing.
	if image, _ := javaImageFor("eclipse-temurin:21-jre", dir, map[string]string{"SERVER_JARFILE": "../../etc/x.jar"}); image != "eclipse-temurin:21-jre" {
		t.Fatalf("got %s", image)
	}
	writeTestJar(t, filepath.Join(dir, "old.jar"), 61)
	if image, _ := javaImageFor("eclipse-temurin:21-jre", dir, map[string]string{"SERVER_JARFILE": "old.jar"}); image != "eclipse-temurin:21-jre" {
		t.Fatalf("got %s", image)
	}
}
