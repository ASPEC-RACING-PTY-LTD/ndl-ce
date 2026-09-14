package gameserver

import (
	"fmt"
	"strconv"
	"strings"
)

func expandStartup(startup string, env map[string]string, memoryMB int, port int) string {
	repl := map[string]string{
		"SERVER_MEMORY": strconv.Itoa(memoryMB),
		"SERVER_PORT":   strconv.Itoa(port),
	}
	for k, v := range env {
		repl[k] = v
	}
	out := startup
	for k, v := range repl {
		out = strings.ReplaceAll(out, "{{"+k+"}}", v)
		out = strings.ReplaceAll(out, "${"+k+"}", v)
	}
	return out
}

func primaryPort(ports []Port) int {
	for _, p := range ports {
		if p.Primary && p.HostPort > 0 {
			return p.HostPort
		}
		if p.Primary && p.ContainerPort > 0 {
			return p.ContainerPort
		}
	}
	for _, p := range ports {
		if p.HostPort > 0 {
			return p.HostPort
		}
		if p.ContainerPort > 0 {
			return p.ContainerPort
		}
	}
	return 0
}

func validateEnv(t Template, env map[string]string) error {
	for _, v := range t.Variables {
		val := strings.TrimSpace(env[v.Env])
		if v.Required && val == "" {
			return fmt.Errorf("%s is required", v.Name)
		}
		if v.FieldType == "number" && val != "" {
			if _, err := strconv.Atoi(val); err != nil {
				return fmt.Errorf("%s must be a number", v.Name)
			}
		}
	}
	return nil
}

func mergeEnv(t Template, user map[string]string) map[string]string {
	out := map[string]string{}
	for _, v := range t.Variables {
		out[v.Env] = v.Default
	}
	for k, val := range user {
		out[k] = val
	}
	return out
}

func memoryMB(bytes int64) int {
	if bytes <= 0 {
		return 1024
	}
	return int(bytes / (1024 * 1024))
}
