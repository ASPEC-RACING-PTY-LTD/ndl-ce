package oci

import (
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

var (
	envNameRe  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	imageRefRe = regexp.MustCompile(`^[a-zA-Z0-9._\-/:@]+$`)
	protoAllow = map[string]bool{"tcp": true, "udp": true, "": true}
)

// ValidateSpec checks identity, image, ports, env, volumes, and health.
// Privileged-by-default is refused by the HTTP layer; Spec.Privileged may still be true for admin.
func ValidateSpec(spec Spec) error {
	if _, err := uuid.Parse(strings.TrimSpace(spec.WorkloadID)); err != nil {
		return fmt.Errorf("workload_id must be a UUID")
	}
	if err := ValidateImageRef(spec.ImagePin); err != nil {
		return err
	}
	if strings.TrimSpace(spec.Name) == "" {
		return fmt.Errorf("name is required")
	}
	for _, p := range spec.Ports {
		if p.ContainerPort < 1 || p.ContainerPort > 65535 {
			return fmt.Errorf("container_port must be 1-65535")
		}
		if p.HostPort < 0 || p.HostPort > 65535 {
			return fmt.Errorf("host_port must be 0-65535")
		}
		proto := strings.ToLower(strings.TrimSpace(p.Protocol))
		if !protoAllow[proto] {
			return fmt.Errorf("port protocol must be tcp or udp")
		}
	}
	for _, e := range spec.Env {
		if !envNameRe.MatchString(e.Name) {
			return fmt.Errorf("invalid env name %q", e.Name)
		}
		if strings.ContainsAny(e.Value, "\x00\n\r") {
			return fmt.Errorf("env value contains banned characters")
		}
	}
	for _, s := range spec.SecretRefs {
		if !envNameRe.MatchString(s.Name) {
			return fmt.Errorf("invalid secret env name %q", s.Name)
		}
		if _, err := uuid.Parse(strings.TrimSpace(s.SecretID)); err != nil {
			return fmt.Errorf("secret_id must be a UUID")
		}
	}
	for _, v := range spec.Volumes {
		if err := ValidateVolumeMount(v); err != nil {
			return err
		}
	}
	if spec.Health != nil {
		if spec.Health.Port < 0 || spec.Health.Port > 65535 {
			return fmt.Errorf("health port must be 0-65535")
		}
		if spec.Health.HTTPPath != "" && !strings.HasPrefix(spec.Health.HTTPPath, "/") {
			return fmt.Errorf("health http_path must start with /")
		}
	}
	for _, d := range spec.GPUDevices {
		if !strings.HasPrefix(d, "/dev/") {
			return fmt.Errorf("gpu device must be a /dev/ node")
		}
		if d == "/" || d == "/dev" {
			return fmt.Errorf("gpu device path is invalid")
		}
	}
	return nil
}

// ValidateImageRef accepts a registry path or docker-style reference.
func ValidateImageRef(ref string) error {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return fmt.Errorf("image_pin is required")
	}
	if strings.ContainsAny(ref, " \t\n\r;`|$&<>\"'\\") {
		return fmt.Errorf("image_pin contains banned characters")
	}
	if !imageRefRe.MatchString(ref) {
		return fmt.Errorf("image_pin is invalid")
	}
	if strings.HasPrefix(ref, "/") || strings.Contains(ref, "..") {
		return fmt.Errorf("image_pin path is invalid")
	}
	return nil
}

// ValidateVolumeMount requires a No-dal volume UUID and refuses host path /.
func ValidateVolumeMount(v VolumeMount) error {
	if strings.TrimSpace(v.HostPath) != "" {
		cleaned := path.Clean(v.HostPath)
		if cleaned == "/" {
			return fmt.Errorf("host bind to / is not allowed")
		}
		return fmt.Errorf("host_path mounts are not allowed; use volume_id")
	}
	if _, err := uuid.Parse(strings.TrimSpace(v.VolumeID)); err != nil {
		return fmt.Errorf("volume_id must be a No-dal volume UUID")
	}
	cp := strings.TrimSpace(v.ContainerPath)
	if cp == "" || !strings.HasPrefix(cp, "/") {
		return fmt.Errorf("container_path must be an absolute path")
	}
	if path.Clean(cp) == "/" {
		return fmt.Errorf("container_path must not be /")
	}
	return nil
}

// ValidateRegistryURL accepts https registry endpoints. http is only for insecure test fixtures.
func ValidateRegistryURL(raw string, allowInsecure bool) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("registry url is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("registry url is invalid")
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return nil
	case "http":
		if allowInsecure {
			return nil
		}
		return fmt.Errorf("registry url must be https")
	default:
		return fmt.Errorf("registry url must be https")
	}
}
