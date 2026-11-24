// startup_test.go 验证服务可访问后才交给浏览器启动器，真实交换会话且正常日志不泄漏密钥。
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestStartupOpensAuthenticatedPage(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var output bytes.Buffer
	opened := 0
	err := run(ctx, "127.0.0.1:0", t.TempDir(), true, func(target string) error {
		defer cancel()
		opened++
		u, err := url.Parse(target)
		if err != nil || !strings.HasPrefix(u.Host, "127.0.0.1:") || !strings.HasPrefix(u.Fragment, "token=") {
			t.Error("invalid automatic local link")
			return errors.New("invalid link")
		}
		token := strings.TrimPrefix(u.Fragment, "token=")
		base := u.Scheme + "://" + u.Host
		jar, _ := cookiejar.New(nil)
		client := &http.Client{Jar: jar, Timeout: 2 * time.Second}
		for _, tc := range []struct {
			path string
			code int
		}{{"/", 200}, {"/api/overview", 401}} {
			response, err := client.Get(base + tc.path)
			if err != nil {
				t.Error("service was not ready before browser launch", err)
				return err
			}
			response.Body.Close()
			if response.StatusCode != tc.code {
				t.Error(tc.path, response.StatusCode)
			}
		}
		body, _ := json.Marshal(map[string]string{"token": token})
		request, _ := http.NewRequest("POST", base+"/api/session", bytes.NewReader(body))
		request.Header.Set("Origin", base)
		request.Header.Set("Content-Type", "application/json")
		response, err := client.Do(request)
		if err != nil {
			t.Error(err)
			return err
		}
		response.Body.Close()
		if response.StatusCode != 200 {
			t.Error("automatic session exchange failed", response.StatusCode)
		}
		response, err = client.Get(base + "/api/overview")
		if err != nil {
			t.Error(err)
			return err
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			t.Error("session was not usable", response.StatusCode)
		}
		return nil
	}, &output)
	if err != nil || opened != 1 || strings.Contains(output.String(), "#token=") || !strings.Contains(output.String(), "自动连接") {
		t.Fatal("automatic launch contract failed", opened, err)
	}
}

func TestStartupFallback(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		var output bytes.Buffer
		calls := 0
		announceStartup(&output, "127.0.0.1:8183", "test-token", automatic, func(string) error {
			calls++
			return errors.New("no browser")
		})
		if !strings.Contains(output.String(), "http://127.0.0.1:8183/#token=test-token") || (automatic && calls != 1) || (!automatic && calls != 0) {
			t.Fatal("manual fallback missing or disabled launch ran", calls)
		}
	}
	if err := run(context.Background(), "0.0.0.0:8183", t.TempDir(), true, func(string) error { t.Error("unsafe bind opened browser"); return nil }, io.Discard); err == nil {
		t.Fatal("non-loopback listener accepted")
	}
}
