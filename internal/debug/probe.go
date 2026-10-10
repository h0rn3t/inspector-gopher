// Probe checks whether a DAP server answers at addr.
package debug

import (
	"context"
	"time"
)

// Probe dials addr and issues initialize; true means a DAP server answered.
func Probe(ctx context.Context, addr string, timeout time.Duration) bool {
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	raw, err := dialTCP(ctx, addr)
	if err != nil {
		return false
	}
	client := newDAPClient(raw)
	defer client.close()
	if _, err := client.send(ctx, "initialize", map[string]any{"adapterID": "inspector-gopher-probe"}); err != nil {
		return false
	}
	return true
}
