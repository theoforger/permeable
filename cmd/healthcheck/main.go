// Command healthcheck is a tiny standalone probe for GET /healthz,
// meant to be invoked (exec form, no shell) as the container's
// HEALTHCHECK. It exists because the distroless final image has no
// shell/curl/wget for a normal `CMD curl ...` healthcheck — this binary
// is the entire mechanism.
package main

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"
)

func main() {
	port := 8080
	if v := os.Getenv("PERMEABLE_ADMIN_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			port = p
		}
	}

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/healthz", port))
	if err != nil {
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		os.Exit(1)
	}
}
