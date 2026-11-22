package exchangex

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCleanErrKeepsNotFound(t *testing.T) {
	err := CleanErr(fmt.Errorf("binance: key KEY123456: %w", ErrOrderNotFound), "KEY123456")
	if !errors.Is(err, ErrOrderNotFound) || strings.Contains(err.Error(), "KEY123456") {
		t.Fatalf("%v", err)
	}
	if errors.Unwrap(CleanErr(errors.New("x SECRET1234"), "SECRET1234")) != nil {
		t.Fatal("the raw error can be unwrapped")
	}
}

func TestCleanErrDropsTheQuery(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	u := srv.URL
	srv.Close()
	_, err := http.Get(u + "/x?timestamp=1&signature=abcdef&apikey=KEY99999") // #nosec G107 -- local closed test server
	got := CleanErr(err, "KEY99999").Error()
	for _, s := range []string{"signature", "abcdef", "KEY99999", "timestamp"} {
		if strings.Contains(got, s) {
			t.Fatalf("%q holds %q", got, s)
		}
	}
}
