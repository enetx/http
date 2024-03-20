package http_test

import (
	"log"
	"net/url"
	"strings"
	"testing"

	"github.com/enetx/http"
	"github.com/enetx/http/cookiejar"
	"github.com/enetx/http/httptest"
	"golang.org/x/net/publicsuffix"
)

// Tests if content-length header is present in request headers during POST
func TestContentLength(t *testing.T) {
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if hdr, ok := r.Header["Content-Length"]; ok {
			if len(hdr) != 1 {
				t.Fatalf("Got %v content-length headers, should only be 1", len(hdr))
			}
			return
		}
		log.Printf("Proto: %v", r.Proto)
		for name, value := range r.Header {
			log.Printf("%v: %v", name, value)
		}
		t.Fatalf("Could not find content-length header")
	}))

	ts.EnableHTTP2 = true

	ts.StartTLS()
	defer ts.Close()

	form := url.Values{}
	form.Add("Hello", "World")

	req, err := http.NewRequest("POST", ts.URL, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf(err.Error())
	}

	req.Header.Add("user-agent", "Go Testing")

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf(err.Error())
	}

	resp.Body.Close()
}

// TestClient_Cookies tests whether set cookies are being sent
func TestClient_SendsCookies(t *testing.T) {
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("cookie")
		if err != nil {
			t.Fatalf(err.Error())
		}
		if cookie.Value == "" {
			t.Fatalf("Cookie value is empty")
		}
	}))

	ts.EnableHTTP2 = true

	ts.StartTLS()
	defer ts.Close()

	jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	if err != nil {
		t.Fatalf(err.Error())
	}

	c := ts.Client()
	c.Jar = jar

	ur := ts.URL

	u, err := url.Parse(ur)
	if err != nil {
		t.Fatalf(err.Error())
	}

	cookies := []*http.Cookie{{Name: "cookie", Value: "Hello world"}}
	jar.SetCookies(u, cookies)

	resp, err := c.Get(ur)
	if err != nil {
		t.Fatalf(err.Error())
	}

	resp.Body.Close()
}
