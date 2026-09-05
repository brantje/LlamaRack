package resourceid

import (
	"strings"
	"sync"
)

// instancePublicSlugs is a process-local identity index used by components that
// own durable Instance IDs but still need the public OpenAI alias (notably the
// worker supervisor). It is populated by Instance service reads/writes and is
// never a persistence source of truth.
var instancePublicSlugs sync.Map

func RememberInstanceSlug(id, slug string) {
	id, slug = strings.TrimSpace(id), strings.TrimSpace(slug)
	if id == "" || slug == "" {
		return
	}
	instancePublicSlugs.Store(id, slug)
}

func ForgetInstanceSlug(id string) {
	if id = strings.TrimSpace(id); id != "" {
		instancePublicSlugs.Delete(id)
	}
}

func InstanceSlug(id string) string {
	if value, ok := instancePublicSlugs.Load(strings.TrimSpace(id)); ok {
		if slug, ok := value.(string); ok {
			return slug
		}
	}
	return ""
}
