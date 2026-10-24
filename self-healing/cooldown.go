package main

import (
	"sync"
	"time"
)

// Cooldown impede restarts em sequência no mesmo Deployment (evita loop de
// restart quando o alerta continua em Firing — "alert fatigue" automatizada).
// O estado é em memória: por isso o receptor roda com 1 réplica.
type Cooldown struct {
	mu     sync.Mutex
	last   map[string]time.Time
	window time.Duration
	now    func() time.Time
}

func newCooldown(window time.Duration) *Cooldown {
	return &Cooldown{last: map[string]time.Time{}, window: window, now: time.Now}
}

// TryAcquire reserva a janela para key. Se ainda estiver em cooldown, devolve
// false e o tempo restante. A reserva é atômica, então dois webhooks simultâneos
// não disparam dois restarts.
func (c *Cooldown) TryAcquire(key string) (bool, time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	if last, ok := c.last[key]; ok {
		if elapsed := now.Sub(last); elapsed < c.window {
			return false, c.window - elapsed
		}
	}
	c.last[key] = now
	return true, 0
}

// Release desfaz a reserva (usado quando o restart falhou: não faz sentido
// bloquear uma nova tentativa por causa de uma ação que não aconteceu).
func (c *Cooldown) Release(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.last, key)
}
