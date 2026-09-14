package gameserver

import (
	"context"
	"net/http"
	"strings"
)

func (t Template) ImageFor(label string) string {
	return t.imageFor(label)
}

func ExpandStartup(startup string, env map[string]string, memoryMB int, port int) string {
	return expandStartup(startup, env, memoryMB, port)
}

func MemoryMB(bytes int64) int {
	return memoryMB(bytes)
}

func PrimaryPort(ports []Port) int {
	return primaryPort(ports)
}

func RedactEnv(env map[string]string) map[string]string {
	return redactMap(env)
}

func RedactLog(text string, env map[string]string) string {
	return redactText(text, secretValues(env))
}

func MatchCatalogue(items []CatalogueItem, q string) []CatalogueItem {
	return matchCatalogue(items, q)
}

func FilterCatalogue(items []CatalogueItem, group string) []CatalogueItem {
	return filterCatalogue(items, group)
}

func BuiltinCatalogue() []CatalogueItem {
	return builtinCatalogue()
}

func ImportFromURL(ctx context.Context, client *http.Client, raw string) (Template, error) {
	return importFromURL(ctx, client, raw)
}

func ExpandGithubDir(ctx context.Context, client *http.Client, raw string) ([]CatalogueItem, error) {
	return expandGithubDir(ctx, client, raw)
}

func ValidatePublicURL(raw string) error {
	return validatePublicURL(raw)
}

func ApplyFriendly(settings []Setting, values map[string]string, files map[string]string, env map[string]string) (map[string]string, map[string]string, error) {
	return applyFriendly(settings, values, files, env)
}

func ReadFriendly(settings []Setting, files map[string]string, env map[string]string) map[string]string {
	return readFriendly(settings, files, env)
}

func DiffSettings(settings []Setting, before, after map[string]string) []ConfigChange {
	return diffSettings(settings, before, after)
}

func ProviderFor(spec ContentSpec) ContentProvider {
	return providerFor(spec)
}

func SafeContentName(name string) string {
	return safeContentName(name)
}

func HasCapability(caps []string, want string) bool {
	return hasCap(caps, want)
}

func HTTPClient() *http.Client {
	return defaultHTTPClient()
}

func TemplateByID(id string) (Template, bool) {
	return templateByID(id)
}

func SecretKeys(env map[string]string) []string {
	var out []string
	for k := range env {
		if looksSecretName(k) {
			out = append(out, k)
		}
	}
	return out
}

func LooksSecret(name string) bool {
	return looksSecretName(name)
}

func MergeEnv(t Template, user map[string]string) map[string]string {
	return mergeEnv(t, user)
}

func ValidateEnv(t Template, env map[string]string) error {
	return validateEnv(t, env)
}

func Clip(s string, n int) string {
	return clip(s, n)
}

func FirstNonEmpty(parts ...string) string {
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			return p
		}
	}
	return ""
}
