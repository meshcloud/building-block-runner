package mockgithub

import (
	"crypto/rand"
	"sync"
	"time"
)

type issuedTokens struct {
	mu     sync.Mutex
	expiry map[string]time.Time
}

func (i *issuedTokens) issue(lifetime time.Duration) string {
	i.mu.Lock()
	defer i.mu.Unlock()
	token := "ghs_" + rand.Text()
	i.expiry[token] = time.Now().Add(lifetime)
	return token
}

func (i *issuedTokens) valid(token string) bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	expiry, issued := i.expiry[token]
	return issued && time.Now().Before(expiry)
}

func (i *issuedTokens) count() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return len(i.expiry)
}
