// Command kavcheck runs rmKavita's sign-in (login, then the OPDS key check)
// against a Kavita server from a computer and prints what happened at each
// step, with the key and password removed. It is for diagnosing a sign-in that
// fails on a device, without the device.
//
//	go run ./cmd/kavcheck -server http://host:5000 -user NAME [-proxy socks5://127.0.0.1:1055]
//
// The password is read from the terminal without echo.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"rmkavita/internal/opds"
	"rmkavita/internal/store"
)

func main() {
	server := flag.String("server", "", "Kavita address, as typed on the device")
	user := flag.String("user", "", "Kavita user name")
	proxy := flag.String("proxy", "", "optional proxy (socks5://127.0.0.1:1055)")
	ua := flag.String("ua", "rmKavita/0.4 (Kobo)", "User-Agent")
	flag.Parse()
	if *server == "" {
		flag.Usage()
		os.Exit(2)
	}
	in := bufio.NewReader(os.Stdin)
	if *user == "" {
		fmt.Print("Kavita user name: ")
		l, _ := in.ReadString('\n')
		*user = strings.TrimSpace(l)
	}
	srv, err := store.NormalizeServer(*server)
	if err != nil {
		fmt.Println("address:", err)
		os.Exit(1)
	}
	fmt.Println("address:", srv)
	fmt.Print("password (not shown): ")
	pw := readSecret(in)
	fmt.Println()

	c := opds.NewUnsigned()
	c.UA = *ua
	if *proxy != "" {
		pu, err := url.Parse(*proxy)
		if err != nil {
			fmt.Println("proxy:", err)
			os.Exit(1)
		}
		c.SetProxy(pu)
	}
	base, _ := url.Parse(srv)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	t0 := time.Now()
	res, used, e := c.LoginAuto(ctx, base, *user, pw, !strings.Contains(*server, "://"))
	if used.Scheme != base.Scheme {
		fmt.Println("switched to", used.Scheme+"://"+used.Host)
		base = used
	}
	if e != nil {
		fmt.Printf("1 login:     FAILED kind=%d status=%d %q (%d ms)\n", e.Kind, e.Status, e.Msg, time.Since(t0).Milliseconds())
		os.Exit(1)
	}
	fmt.Printf("1 login:     ok, user %q, key from %s, %d chars (%d ms)\n", res.Username, res.KeySource, len(res.Key), time.Since(t0).Milliseconds())

	t1 := time.Now()
	if e := c.VerifyKey(ctx, base, res.Key); e != nil {
		c.Configure(base, res.Key) // so the key is redacted from anything printed
		fmt.Printf("2 key check: FAILED kind=%d status=%d %q (%d ms)\n", e.Kind, e.Status, c.Redact(e.Msg), time.Since(t1).Milliseconds())
		os.Exit(1)
	}
	fmt.Printf("2 key check: ok, the OPDS catalogue opened (%d ms)\n", time.Since(t1).Milliseconds())
	fmt.Println("sign-in works")

	// 3: the libraries this user can see (what rmKavita's Libraries list shows)
	c.Configure(base, res.Key)
	root, err := c.GetFeed(ctx, opds.RootURL(base, res.Key))
	if err != nil {
		fmt.Printf("3 libraries: FAILED reading the catalogue: %s\n", c.Redact(err.Error()))
		os.Exit(1)
	}
	libURL := ""
	for _, en := range root.Entries {
		if en.ID == "allLibraries" || strings.EqualFold(en.Title, "All Libraries") {
			for _, l := range en.Links {
				if l.Href != "" {
					libURL, _ = c.Resolve(l.Href, opds.RootURL(base, res.Key))
					break
				}
			}
		}
	}
	if libURL == "" {
		fmt.Println("3 libraries: the catalogue has no \"All Libraries\" entry")
		os.Exit(1)
	}
	lf, err := c.GetFeed(ctx, libURL)
	if err != nil {
		fmt.Printf("3 libraries: FAILED: %s\n", c.Redact(err.Error()))
		os.Exit(1)
	}
	fmt.Printf("3 libraries: %d visible to %q\n", len(lf.Entries), res.Username)
	for _, en := range lf.Entries {
		fmt.Println("   -", en.Title)
	}
	if len(lf.Entries) == 0 {
		fmt.Println("   Kavita gives this user no libraries: in Kavita, Settings > Users > this user > Libraries, tick the ones to share")
	}
}

func readSecret(in *bufio.Reader) string {
	restore := noEcho()
	defer restore()
	line, _ := in.ReadString('\n')
	return strings.TrimRight(line, "\r\n")
}
