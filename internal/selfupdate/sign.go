package selfupdate

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
)

// sigAsset is the detached Ed25519 signature over the SHA256SUMS asset.
const sigAsset = "SHA256SUMS.sig"

// PublicKey is the base64 Ed25519 release-signing public key. PLACEHOLDER: it
// is empty until the key is generated and the release workflow signs
// SHA256SUMS (see SECURITY.md, "Release signing"). While empty, signature
// verification is skipped with a notice.
var PublicKey = ""

// RequireSignature makes a release without SHA256SUMS.sig a refusal instead of
// a warning. It is flipped one release after signing starts.
const RequireSignature = false

func parseKey(s string) (ed25519.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("embedded release public key is not a base64 Ed25519 key")
	}
	return ed25519.PublicKey(raw), nil
}

func decodeSig(blob []byte) []byte {
	if len(blob) == ed25519.SignatureSize {
		return blob
	}
	if raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(blob))); err == nil {
		return raw
	}
	return blob
}

// checkSignature verifies sums against sig. A nil sig means the release
// carries none.
func checkSignature(out io.Writer, key string, require bool, sums, sig []byte) error {
	if strings.TrimSpace(key) == "" {
		_, _ = fmt.Fprintln(out, "[notice] release signature verification is not enabled in this build; relying on SHA256SUMS alone")
		return nil
	}
	pub, err := parseKey(key)
	if err != nil {
		return err
	}
	if sig == nil {
		if require {
			return fmt.Errorf("release has no %s; refusing to install an unsigned release", sigAsset)
		}
		_, _ = fmt.Fprintf(out, "[notice] release has no %s; this will be refused in a future version\n", sigAsset)
		return nil
	}
	if !ed25519.Verify(pub, sums, decodeSig(sig)) {
		return fmt.Errorf("%s does not verify against the embedded release key; refusing to install", sigAsset)
	}
	_, _ = fmt.Fprintln(out, "  verified release signature")
	return nil
}
