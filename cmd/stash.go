package cmd

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"sync"
	"time"

	stash "github.com/Anastylosis/stash-go"
	"github.com/spf13/cobra"

	"github.com/Anastylosis/FSS/internal/httpx"
)

var stashCmd = &cobra.Command{
	Use:   "stash",
	Short: "Interact with a Stash instance",
}

func init() {
	rootCmd.AddCommand(stashCmd)

	stashCmd.PersistentFlags().String("url", "", "Stash server URL (default from config)")
	stashCmd.PersistentFlags().String("api-key", "", "Stash API key (env: FSS_STASH_API_KEY)")
}

// stashRequestTimeout bounds one GraphQL request. Retries get the full budget
// each, since it is per attempt.
const stashRequestTimeout = 120 * time.Second

// newStashClient builds the client every stash subcommand uses, on fss's
// pooled, retrying transport rather than the library's plain default.
func newStashClient(cmd *cobra.Command) *stash.Client {
	return stash.NewClient(stashURL(cmd),
		stash.WithAPIKey(stashAPIKey(cmd)),
		stash.WithHTTPClient(httpx.NewGraphQLRetryClient(stashRequestTimeout)),
	)
}

func stashURL(cmd *cobra.Command) string {
	u, _ := cmd.Flags().GetString("url")
	if u == "" {
		u = cfg.Stash.URL
	}
	warnIfRemoteStash(u)
	return u
}

var remoteStashOnce sync.Once

func warnIfRemoteStash(rawURL string) {
	if rawURL == "" {
		return
	}
	remoteStashOnce.Do(func() {
		u, err := url.Parse(rawURL)
		if err != nil {
			return
		}
		host := u.Hostname()
		if host == "" {
			return
		}
		if ip := net.ParseIP(host); ip != nil {
			if !isLocalOrPrivate(ip) {
				fmt.Fprintf(os.Stderr, "[warn] stash URL %q points to a non-local address; API key will be sent there\n", rawURL)
			}
			return
		}
		ips, err := net.LookupIP(host)
		if err != nil {
			return
		}
		for _, ip := range ips {
			if !isLocalOrPrivate(ip) {
				fmt.Fprintf(os.Stderr, "[warn] stash URL %q resolves to non-local IP %s; API key will be sent there\n", rawURL, ip)
				return
			}
		}
	})
}

func isLocalOrPrivate(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified()
}

var apiKeyFlagOnce sync.Once

func stashAPIKey(cmd *cobra.Command) string {
	k, _ := cmd.Flags().GetString("api-key")
	if k != "" {
		apiKeyFlagOnce.Do(func() {
			fmt.Fprintln(os.Stderr, "[warn] --api-key is visible in shell history and process listings; prefer FSS_STASH_API_KEY env var or stash.api_key in config.yaml")
		})
		return k
	}
	if env := os.Getenv("FSS_STASH_API_KEY"); env != "" {
		return env
	}
	return cfg.Stash.APIKey
}
