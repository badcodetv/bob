package main

// mint prints a session cookie for MINT_EMAIL, or the first admin in BOB_PROJECT_MAP.
import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"

	"github.com/badcodetv/bob/internal/auth"
)

func main() {
	email := os.Getenv("MINT_EMAIL")
	if email == "" {
		var m map[string][]string
		json.Unmarshal([]byte(os.Getenv("BOB_PROJECT_MAP")), &m)
		for e, ps := range m {
			if len(ps) > 0 && ps[0] == "*" {
				email = e
			}
		}
	}
	a := &auth.Auth{Secret: []byte(os.Getenv("BOB_SESSION_SECRET"))}
	rec := httptest.NewRecorder()
	a.SetSession(rec, email)
	c := rec.Result().Cookies()[0]
	fmt.Print(c.Name + "=" + c.Value)
}
