package main

import (
	"bufio"
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

const (
	service    = "s3"
	listenAddr = ":9000"
)

var (
	accessKey    string
	secretKey    string
	region       string
	upstreamHost string
	upstreamBase string
)

// loadEnv reads KEY=VALUE pairs from a .env file and sets them as environment variables.
func loadEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return // .env is optional; env vars may already be set
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.IndexByte(line, '=')
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		// Strip optional surrounding quotes
		if len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
			val = val[1 : len(val)-1]
		}
		os.Setenv(key, val)
	}
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h)
}

func signingKey(date string) []byte {
	k := hmacSHA256([]byte("AWS4"+secretKey), date)
	k = hmacSHA256(k, region)
	k = hmacSHA256(k, service)
	k = hmacSHA256(k, "aws4_request")
	return k
}

func signRequest(method, rawPath, rawQuery string, body []byte, now time.Time) http.Header {
	amzDate := now.UTC().Format("20060102T150405Z")
	dateStamp := now.UTC().Format("20060102")
	bodyHash := sha256Hex(body)

	// Canonical query string (sorted)
	params, _ := url.ParseQuery(rawQuery)
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var qparts []string
	for _, k := range keys {
		for _, v := range params[k] {
			qparts = append(qparts, url.QueryEscape(k)+"="+url.QueryEscape(v))
		}
	}
	canonicalQuery := strings.Join(qparts, "&")

	// Only sign minimal headers (host, x-amz-content-sha256, x-amz-date)
	canonicalHeaders := fmt.Sprintf("host:%s\nx-amz-content-sha256:%s\nx-amz-date:%s\n",
		upstreamHost, bodyHash, amzDate)
	signedHeaders := "host;x-amz-content-sha256;x-amz-date"

	canonicalRequest := strings.Join([]string{
		method, rawPath, canonicalQuery,
		canonicalHeaders, signedHeaders, bodyHash,
	}, "\n")

	credentialScope := fmt.Sprintf("%s/%s/%s/aws4_request", dateStamp, region, service)
	stringToSign := fmt.Sprintf("AWS4-HMAC-SHA256\n%s\n%s\n%s",
		amzDate, credentialScope, sha256Hex([]byte(canonicalRequest)))

	sig := fmt.Sprintf("%x", hmacSHA256(signingKey(dateStamp), stringToSign))
	auth := fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		accessKey, credentialScope, signedHeaders, sig)

	h := http.Header{}
	h.Set("Authorization", auth)
	h.Set("X-Amz-Date", amzDate)
	h.Set("X-Amz-Content-Sha256", bodyHash)
	return h
}

func proxy(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body error", 500)
		return
	}

	target := upstreamBase + r.URL.Path
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}

	authHeaders := signRequest(r.Method, r.URL.Path, r.URL.RawQuery, body, time.Now())

	req, err := http.NewRequest(r.Method, target, bytes.NewReader(body))
	if err != nil {
		http.Error(w, "create request error", 500)
		return
	}

	// Forward content-type from original request (needed for PUT)
	if ct := r.Header.Get("Content-Type"); ct != "" {
		req.Header.Set("Content-Type", ct)
	}

	// Apply signed headers
	for k, v := range authHeaders {
		req.Header[k] = v
	}

	// Add Accept-Encoding: identity AFTER signing so it's not in SignedHeaders
	// This prevents Cloudflare from gzip-compressing responses (which removes Content-Length)
	req.Header.Set("Accept-Encoding", "identity")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "upstream error: "+err.Error(), 502)
		return
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, "read response error", 502)
		return
	}

	// Copy upstream headers, skip hop-by-hop headers
	skip := map[string]bool{
		"Content-Encoding": true,
		"Transfer-Encoding": true,
		"Connection":        true,
		"Server":            true,
	}
	for k, vs := range resp.Header {
		if skip[k] {
			continue
		}
		for _, v := range vs {
			// RunPod returns "UTC" instead of standard "GMT" — fix it for rclone
			if strings.EqualFold(k, "Last-Modified") || strings.EqualFold(k, "Date") {
				v = strings.Replace(v, " UTC", " GMT", 1)
			}
			w.Header().Add(k, v)
		}
	}

	// For HEAD: Flask/net/http would set Content-Length from body (0).
	// We already got the real Content-Length from the upstream response above.
	// net/http will set it correctly from len(respBody) which is 0 for HEAD,
	// so we explicitly set it from the upstream header.
	if r.Method == http.MethodHead {
		if cl := resp.Header.Get("Content-Length"); cl != "" {
			w.Header().Set("Content-Length", cl)
		}
	}

	w.WriteHeader(resp.StatusCode)
	if r.Method != http.MethodHead {
		w.Write(respBody)
	}

	log.Printf("%s %s%s → %d (%s) [%dms]",
		r.Method, r.URL.Path,
		func() string {
			if r.URL.RawQuery != "" {
				return "?" + r.URL.RawQuery
			}
			return ""
		}(),
		resp.StatusCode,
		http.StatusText(resp.StatusCode),
		time.Since(start).Milliseconds(),
	)
}

func main() {
	loadEnv(".env")
	accessKey = os.Getenv("RUNPOD_ACCESS_KEY")
	secretKey = os.Getenv("RUNPOD_SECRET_KEY")
	region = os.Getenv("RUNPOD_REGION")
	if accessKey == "" || secretKey == "" || region == "" {
		log.Fatal("RUNPOD_ACCESS_KEY, RUNPOD_SECRET_KEY, and RUNPOD_REGION must be set (in .env or environment)")
	}
	upstreamHost = fmt.Sprintf("s3api-%s.runpod.io", region)
	upstreamBase = fmt.Sprintf("https://s3api-%s.runpod.io", region)

	addr := listenAddr
	if len(os.Args) > 1 {
		addr = os.Args[1]
	}
	log.Printf("RunPod S3 proxy listening on http://localhost%s", addr)
	log.Printf("Use rclone with --s3-endpoint http://localhost%s --s3-no-check-bucket", addr)
	http.HandleFunc("/", proxy)
	log.Fatal(http.ListenAndServe(addr, nil))
}
