package githubdownload

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type closingResponseBody struct {
	io.Reader
	closes   int
	closeErr error
}

func (b *closingResponseBody) Close() error {
	b.closes++
	return b.closeErr
}

func TestResponseCloseGuardFailureRejectsBytesWithoutFallback(t *testing.T) {
	for _, source := range []string{
		"https://api.github.com/repos/example/tool/releases/latest",
		"https://github.com/example/tool/releases/download/v1.0.0/tool",
	} {
		for _, policy := range []string{"direct", "https://file-only.invalid/public"} {
			for _, guard := range []string{"binding", "instance"} {
				t.Run(source+"/"+policy+"/"+guard, func(t *testing.T) {
					// Reads, including EOF, succeed. Only the final guard reports
					// that the selected binding or process identity became invalid.
					guardErr := errors.New(guard + " invalid: http://private-user:private-password@private.invalid/?token=private-token")
					body := &closingResponseBody{Reader: strings.NewReader("complete response"), closeErr: guardErr}
					calls := 0
					client := &http.Client{Transport: transportFunc(func(request *http.Request) (*http.Response, error) {
						calls++
						if calls != 1 {
							t.Fatal("Close guard failure triggered an alternate network route")
						}
						result := response(request, http.StatusOK, "complete response")
						result.Body = body
						return result, nil
					})}
					data, err := Get(client, source, 1024, policy)
					var failure *downloadFailure
					if data != nil || !errors.Is(err, guardErr) || !errors.As(err, &failure) || failure.retry || calls != 1 || body.closes != 1 {
						t.Fatalf("Close failure accepted or retried: data=%q calls=%d closes=%d err=%v", data, calls, body.closes, err)
					}
					if err.Error() != "GitHub 下载响应关闭失败（其他传输错误）" {
						t.Fatalf("unsafe or unexpected Close error text: %v", err)
					}
				})
			}
		}
	}
}

func TestResponseCloseRunsOnceOnSuccessAndReadFailures(t *testing.T) {
	const source = "https://api.github.com/repos/example/tool/releases/latest"
	for _, mode := range []string{"success", "status", "declared-oversize", "body-oversize", "read-error", "length-mismatch"} {
		t.Run(mode, func(t *testing.T) {
			body := &closingResponseBody{Reader: strings.NewReader("complete")}
			client := &http.Client{Transport: transportFunc(func(request *http.Request) (*http.Response, error) {
				result := response(request, http.StatusOK, "complete")
				result.Body = body
				switch mode {
				case "status":
					result.StatusCode = http.StatusBadGateway
				case "declared-oversize":
					result.ContentLength = 33
				case "body-oversize":
					body.Reader = strings.NewReader(strings.Repeat("x", 33))
					result.ContentLength = -1
				case "read-error":
					body.Reader = failingReader{io.ErrUnexpectedEOF}
				case "length-mismatch":
					result.ContentLength++
				}
				if mode != "success" {
					// Cleanup must still happen once and preserve the original
					// failure when both reading and closing report errors.
					body.closeErr = errors.New("secondary close failure")
				}
				return result, nil
			})}
			data, err := Get(client, source, 32, "direct")
			if (err == nil) != (mode == "success") || body.closes != 1 {
				t.Fatalf("mode=%s data=%q closes=%d err=%v", mode, data, body.closes, err)
			}
			if mode == "success" && string(data) != "complete" {
				t.Fatalf("success lost bytes: %q", data)
			}
			if mode != "success" && (data != nil || strings.Contains(err.Error(), "secondary close failure")) {
				t.Fatalf("cleanup replaced or exposed original failure: data=%q err=%v", data, err)
			}
		})
	}
}
