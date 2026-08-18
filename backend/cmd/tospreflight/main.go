// Command tospreflight verifies the private-TOS path used before a real
// Seedance run. It uploads only a random probe, checks its signed GET URL,
// then deletes it; it never submits an Ark request.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/volcengine/ve-tos-golang-sdk/v2/tos"
	"github.com/volcengine/ve-tos-golang-sdk/v2/tos/enum"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/media"
)

func main() {
	lifecycleDays := flag.Int("lifecycle-days", 30, "expected enabled lifecycle expiration in days for the configured object prefix")
	skipLifecycle := flag.Bool("skip-lifecycle", false, "skip read-only lifecycle verification for a connectivity-only preflight")
	flag.Parse()
	if *lifecycleDays <= 0 || *lifecycleDays > 365 {
		must(fmt.Errorf("-lifecycle-days must be between 1 and 365"))
	}
	cwd, err := os.Getwd()
	must(err)
	repoRoot := config.DiscoverDevRepoRoot(cwd)
	must(config.LoadDotEnvFiles(config.DefaultDotEnvPaths(repoRoot)...))
	tosConfig, configured := media.TOSAssetPublisherConfigFromEnv(os.Getenv)
	if !configured {
		must(fmt.Errorf("TOS configuration is missing; configure VOLC_TOS_* locally before running this command"))
	}

	client, err := tos.NewClientV2(tosConfig.Endpoint, tos.WithRegion(tosConfig.Region), tos.WithCredentials(tos.NewStaticCredentials(tosConfig.AccessKey, tosConfig.SecretKey)))
	must(err)

	nonce := make([]byte, 16)
	_, err = rand.Read(nonce)
	must(err)
	payload := []byte("cascade-tos-preflight-v1\n" + hex.EncodeToString(nonce) + "\n")
	sum := sha256.Sum256(payload)
	key := strings.Trim(tosConfig.Prefix, "/") + "/preflight/" + time.Now().UTC().Format("20060102T150405.000000000Z") + "-" + hex.EncodeToString(nonce[:6]) + ".txt"
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	_, err = client.PutObjectV2(ctx, &tos.PutObjectV2Input{PutObjectBasicInput: tos.PutObjectBasicInput{
		Bucket: tosConfig.Bucket, Key: key, ContentLength: int64(len(payload)), ContentType: "text/plain",
	}, Content: bytes.NewReader(payload)})
	must(err)

	cleaned := false
	cleanup := func() error {
		if cleaned {
			return nil
		}
		_, err := client.DeleteObjectV2(context.Background(), &tos.DeleteObjectV2Input{Bucket: tosConfig.Bucket, Key: key})
		if err == nil {
			cleaned = true
		}
		return err
	}
	defer func() {
		if err := cleanup(); err != nil {
			fmt.Fprintln(os.Stderr, "TOS preflight cleanup failed; remove the probe object under ark-media/preflight/:", err)
		}
	}()

	signed, err := client.PreSignedURL(&tos.PreSignedURLInput{HTTPMethod: enum.HttpMethodGet, Bucket: tosConfig.Bucket, Key: key, Expires: int64(tosConfig.SignedURLTTL.Seconds())})
	must(err)
	if signed == nil || strings.TrimSpace(signed.SignedUrl) == "" {
		must(fmt.Errorf("TOS preflight did not receive a signed GET URL"))
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, signed.SignedUrl, nil)
	must(err)
	response, err := (&http.Client{Timeout: 30 * time.Second}).Do(request)
	must(err)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		must(fmt.Errorf("signed GET returned HTTP %d", response.StatusCode))
	}
	returned, err := io.ReadAll(io.LimitReader(response.Body, int64(len(payload)+1)))
	must(err)
	returnedSum := sha256.Sum256(returned)
	if !bytes.Equal(returned, payload) || returnedSum != sum {
		must(fmt.Errorf("signed GET content verification failed"))
	}
	must(cleanup())
	if *skipLifecycle {
		fmt.Printf("TOS preflight passed (connectivity-only; lifecycle verification skipped)\n")
		fmt.Printf("bucket=%s\n", tosConfig.Bucket)
		fmt.Printf("key_prefix=%s/preflight/\n", strings.Trim(tosConfig.Prefix, "/"))
		fmt.Printf("uploaded_bytes=%d\n", len(payload))
		fmt.Printf("content_sha256_prefix=%x\n", sum[:6])
		fmt.Printf("signed_get=verified\n")
		fmt.Printf("cleanup=completed\n")
		fmt.Printf("lifecycle=skipped\n")
		return
	}
	lifecycle, err := client.GetBucketLifecycle(ctx, &tos.GetBucketLifecycleInput{Bucket: tosConfig.Bucket})
	must(err)
	lifecycleVerification := media.VerifyTOSLifecycleRules(lifecycle.Rules, tosConfig.Prefix, *lifecycleDays)
	if lifecycleVerification.Status != "verified" {
		fmt.Printf("TOS connectivity/signed_get=verified\n")
		fmt.Printf("TOS lifecycle=status:%s prefix:%s expected_days:%d matched_rule_id:%s reason:%s\n", lifecycleVerification.Status, lifecycleVerification.ExpectedPrefix, lifecycleVerification.ExpectedDays, lifecycleVerification.MatchedRuleID, lifecycleVerification.Reason)
		must(fmt.Errorf("TOS lifecycle rule was not verified; no bucket rule was changed"))
	}

	fmt.Printf("TOS preflight passed\n")
	fmt.Printf("bucket=%s\n", tosConfig.Bucket)
	fmt.Printf("key_prefix=%s/preflight/\n", strings.Trim(tosConfig.Prefix, "/"))
	fmt.Printf("uploaded_bytes=%d\n", len(payload))
	fmt.Printf("content_sha256_prefix=%x\n", sum[:6])
	fmt.Printf("signed_get=verified\n")
	fmt.Printf("cleanup=completed\n")
	fmt.Printf("lifecycle=verified\n")
	fmt.Printf("lifecycle_prefix=%s\n", lifecycleVerification.ExpectedPrefix)
	fmt.Printf("lifecycle_days=%d\n", lifecycleVerification.ExpectedDays)
	fmt.Printf("lifecycle_rule_id=%s\n", lifecycleVerification.MatchedRuleID)
}

func must(err error) {
	if err == nil {
		return
	}
	fmt.Fprintln(os.Stderr, "TOS preflight failed:", err)
	os.Exit(1)
}
