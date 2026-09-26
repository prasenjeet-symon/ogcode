package provider

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strconv"
	"time"
)

// Client assertion header names. A first-party client stamps every request it
// makes to OG Lab's router with these three, and the router refuses an
// unasserted request once it has been given any app secret. The names are an
// agreement between the two hops, so they match the router's spelling exactly.
const (
	headerClientApp       = "X-Client-App"
	headerClientTimestamp = "X-Client-Timestamp"
	headerClientSignature = "X-Client-Signature"
)

// assertionSkew is how far a client's clock may disagree with the router's
// before its assertion is refused.
const assertionSkew = 5 * time.Minute

// assertionPayload is the exact string a client assertion signs. It binds the
// signature to the app, the time it was made, and the particular call, so a
// signature captured for one endpoint does not authorise another and a request
// cannot be replayed outside assertionSkew. It must stay byte-identical to the
// router's copy of the same contract.
func assertionPayload(app, method, path string, ts int64) string {
	return app + "\n" + strconv.FormatInt(ts, 10) + "\n" + method + "\n" + path
}

// signAssertion returns the signature a client presents for one call: the
// base64url HMAC-SHA256 of assertionPayload under its app secret.
func signAssertion(secret, app, method, path string, ts int64) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(assertionPayload(app, method, path, ts)))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// signRequestAssertion stamps req in place with the three assertion headers a
// first-party client presents to the router. An empty app or secret leaves the
// request untouched, so a build without a baked-in secret behaves exactly as
// before.
func signRequestAssertion(req *http.Request, app, secret string) {
	if app == "" || secret == "" {
		return
	}
	ts := time.Now().Unix()
	req.Header.Set(headerClientApp, app)
	req.Header.Set(headerClientTimestamp, strconv.FormatInt(ts, 10))
	req.Header.Set(headerClientSignature, signAssertion(secret, app, req.Method, req.URL.Path, ts))
}
