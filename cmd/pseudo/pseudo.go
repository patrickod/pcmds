// pseudo is a simple web server that generates random two-word phrases/pseudonyms
package main

import (
	"bufio"
	_ "embed"
	"expvar"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"

	"golang.org/x/exp/rand"
	"tailscale.com/tsnet"
	"tailscale.com/tsweb"
	"tailscale.com/words"
)

//go:embed words.txt
var effWordsRaw string

var port = flag.Int("port", 8080, "port to listen on")
var tsDir = flag.String("ts-dir", "", "path to tailscale directory")

var pseudosGenerated = expvar.NewInt("generated")

func init() {
	expvar.Publish("pseudos_generated", pseudosGenerated)
}

func effWords() []string {
	var words []string
	r := strings.NewReader(effWordsRaw)
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Fields(line)
		if len(parts) > 1 {
			words = append(words, parts[1])
		}
	}
	if err := scanner.Err(); err != nil {
		log.Fatalf("failed to read words: %v", err)
	}
	return words
}

func tailscaleWords() []string {
	w := []string{}
	w = append(w, words.Scales()...)
	w = append(w, words.Tails()...)
	return w
}

func randN(min, max int) int {
	return rand.Intn(max-min) + min
}

type server struct {
	effWords []string
	tsWords  []string
}

func (s *server) serveMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	tsweb.Debugger(mux)
	return mux
}

func (s *server) handleIndex(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	ts := (q.Get("ts") == "1")

	words := s.effWords
	if ts {
		words = s.tsWords
	}
	o := []string{}
	for i := 0; i < 2; i++ {
		o = append(o, words[randN(0, len(words))])
	}

	pseudosGenerated.Add(1)
	w.Write([]byte(strings.Join(o, " ")))
}

func main() {
	flag.Parse()

	s := &server{
		effWords: effWords(),
		tsWords:  tailscaleWords(),
	}

	if *tsDir != "" {
		ts := tsnet.Server{
			Hostname: "pseudo",
			Dir:      *tsDir,
		}
		ln, err := ts.Listen("tcp", ":80")
		if err != nil {
			log.Fatalf("failed to listen: %v", err)
		}
		go func() {
			if err := http.Serve(ln, s.serveMux()); err != nil && err != http.ErrServerClosed {
				log.Fatalf("failed to serve: %v", err)
			}
		}()
	}

	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", *port))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	if err := http.Serve(ln, s.serveMux()); err != nil && err != http.ErrServerClosed {
		log.Fatalf("failed to serve: %v", err)
	}
}
