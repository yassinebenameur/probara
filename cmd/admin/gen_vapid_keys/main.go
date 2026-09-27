// Command gen_vapid_keys mints a VAPID keypair for status page browser
// notifications.
//
// The keypair MUST be generated once and supplied as configuration. Letting
// the service generate at startup is always a bug: the status-page deployment
// runs more than one replica, so each would mint a different pair and a
// subscription created against one replica would be unusable by another; and
// a restart would silently invalidate every stored subscription, because push
// services bind an endpoint to the key that created it.
//
// For the same reason, rotating the keypair invalidates every existing
// subscription and every visitor must opt in again.
//
// Usage:
//
//	go run ./cmd/admin/gen_vapid_keys
package main

import (
	"fmt"
	"os"

	"github.com/yassinebenameur/probara/shared/webpush"
)

func main() {
	keys, err := webpush.GenerateKeys()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to generate VAPID keys: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("STATUS_PAGE_VAPID_PUBLIC_KEY=%s\n", keys.Public)
	fmt.Printf("STATUS_PAGE_VAPID_PRIVATE_KEY=%s\n", keys.Private)
	fmt.Println("STATUS_PAGE_VAPID_SUBJECT=mailto:you@example.com")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "The public key ships inside every rendered status page and is not a secret.")
	fmt.Fprintln(os.Stderr, "The private key is a credential: store it in your secret manager.")
	fmt.Fprintln(os.Stderr, "Set SUBJECT to a real mailto: or https: contact -- some push services reject a missing one.")
}
