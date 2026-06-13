// Command gentestjws emits a throwaway Apple-style root certificate plus signed
// StoreKit transaction / notification JWS fixtures, so /subscription/verify and
// /webhooks/apple can be exercised locally without TestFlight or sandbox.
//
// LOCAL TESTING ONLY. Point the server at the emitted root via APPLE_ROOT_CA_PATH.
//
//	go run ./cmd/gentestjws -out /tmp/gf
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mstoews/glutenfree-server/appstore"
	"github.com/mstoews/glutenfree-server/appstore/devsign"
)

func main() {
	out := flag.String("out", ".", "output directory for root.pem and *.jws fixtures")
	bundle := flag.String("bundle", "com.glutenfree.app", "bundle id")
	product := flag.String("product", "com.glutenfree.sub.monthly", "product id")
	original := flag.String("original", "2000000000000001", "originalTransactionId")
	flag.Parse()

	if err := run(*out, *bundle, *product, *original); err != nil {
		fmt.Fprintln(os.Stderr, "gentestjws:", err)
		os.Exit(1)
	}
}

func run(outDir, bundle, product, original string) error {
	chain, err := devsign.NewChain()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	rootPath := filepath.Join(outDir, "root.pem")
	if err := os.WriteFile(rootPath, chain.RootPEM(), 0o644); err != nil {
		return err
	}

	now := time.Now()
	future := now.Add(30 * 24 * time.Hour).UnixMilli()
	past := now.Add(-24 * time.Hour).UnixMilli()

	txn := func(expiresMS int64) *appstore.TransactionPayload {
		return &appstore.TransactionPayload{
			TransactionID:         original,
			OriginalTransactionID: original,
			BundleID:              bundle,
			ProductID:             product,
			PurchaseDateMS:        now.UnixMilli(),
			ExpiresDateMS:         expiresMS,
			Type:                  "Auto-Renewable Subscription",
			InAppOwnershipType:    "PURCHASED",
			Environment:           "Sandbox",
		}
	}

	notif := func(nType string, expiresMS int64) (*appstore.NotificationPayload, error) {
		signedTx, err := chain.SignJWS(txn(expiresMS))
		if err != nil {
			return nil, err
		}
		return &appstore.NotificationPayload{
			NotificationType: nType,
			NotificationUUID: "dev-" + nType,
			Version:          "2.0",
			SignedDateMS:     now.UnixMilli(),
			Data: appstore.NotificationData{
				BundleID:              bundle,
				Environment:           "Sandbox",
				SignedTransactionInfo: signedTx,
			},
		}, nil
	}

	// Active transaction fixture.
	activeJWS, err := chain.SignJWS(txn(future))
	if err != nil {
		return err
	}
	if err := writeJWS(outDir, "txn-active.jws", activeJWS); err != nil {
		return err
	}

	// Notification fixtures: a renewal (active) and an expiry.
	for _, n := range []struct {
		file    string
		nType   string
		expires int64
	}{
		{"notif-renew.jws", "DID_RENEW", future},
		{"notif-expired.jws", "EXPIRED", past},
	} {
		np, err := notif(n.nType, n.expires)
		if err != nil {
			return err
		}
		signed, err := chain.SignJWS(np)
		if err != nil {
			return err
		}
		if err := writeJWS(outDir, n.file, signed); err != nil {
			return err
		}
	}

	fmt.Printf("wrote root.pem + txn-active.jws + notif-renew.jws + notif-expired.jws to %s\n", outDir)
	fmt.Printf("originalTransactionId=%s bundle=%s product=%s\n", original, bundle, product)
	return nil
}

func writeJWS(dir, name, jws string) error {
	return os.WriteFile(filepath.Join(dir, name), []byte(jws), 0o644)
}
