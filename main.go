// Server Jemci untuk Railway (dan tes lokal): API /api/* + file statis dari folder public/.
// Jalankan: go run .   (port dari env PORT, bawaan 8080)
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"jelan-music/internal/ytmusic"
)

type fn func(ctx context.Context, r *http.Request) (any, error)

func need(r *http.Request, k string) (string, error) {
	v := strings.TrimSpace(r.URL.Query().Get(k))
	if v == "" {
		return "", ytmusic.BadReq("parameter " + k + " wajib")
	}
	return v, nil
}

func route(mux *http.ServeMux, path string, cache int, to time.Duration, f fn) {
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		ytmusic.Serve(w, r, cache, to, f)
	})
}

// noCache: halaman utama, service worker, dan manifest selalu dicek ulang oleh browser.
func noCache(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/", "/index.html", "/sw.js", "/manifest.json":
			w.Header().Set("Cache-Control", "no-cache")
		}
		h.ServeHTTP(w, r)
	})
}

func main() {
	e := ytmusic.Engine()
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	route(mux, "/api/search", 600, 30*time.Second, func(c context.Context, r *http.Request) (any, error) {
		q, err := need(r, "q")
		if err != nil {
			return nil, err
		}
		result, err := e.Search(c, q)
		if err != nil {
			return nil, err
		}
		ytmusic.EnrichAppleArtwork(c, q, result)
		return result, nil
	})
	route(mux, "/api/play", 3600, 30*time.Second, func(c context.Context, r *http.Request) (any, error) {
		id, err := need(r, "id")
		if err != nil {
			return nil, err
		}
		return e.PlayMeta(c, id)
	})
	route(mux, "/api/stream", 900, 55*time.Second, func(c context.Context, r *http.Request) (any, error) {
		id, err := need(r, "id")
		if err != nil {
			return nil, err
		}
		return e.Stream(c, id, r.URL.Query().Get("format"))
	})
	route(mux, "/api/dl", 0, 58*time.Second, func(c context.Context, r *http.Request) (any, error) {
		id, err := need(r, "id")
		if err != nil {
			return nil, err
		}
		return e.Download(c, id, r.URL.Query().Get("format"))
	})
	route(mux, "/api/album", 1800, 30*time.Second, func(c context.Context, r *http.Request) (any, error) {
		id, err := need(r, "id")
		if err != nil {
			return nil, err
		}
		return e.Album(c, id)
	})
	route(mux, "/api/playlist", 1800, 30*time.Second, func(c context.Context, r *http.Request) (any, error) {
		id, err := need(r, "id")
		if err != nil {
			return nil, err
		}
		return e.Playlist(c, id)
	})
	route(mux, "/api/mv", 3600, 25*time.Second, func(c context.Context, r *http.Request) (any, error) {
		q, err := need(r, "q")
		if err != nil {
			return nil, err
		}
		return e.VideoSearch(c, q)
	})
	route(mux, "/api/global", 1800, 55*time.Second, func(c context.Context, r *http.Request) (any, error) {
		return e.Global20(c)
	})
	route(mux, "/api/viral", 1800, 45*time.Second, func(c context.Context, r *http.Request) (any, error) {
		return e.Viral20(c)
	})
	route(mux, "/api/genreart", 604800, 25*time.Second, func(c context.Context, r *http.Request) (any, error) {
		g, err := need(r, "g")
		if err != nil {
			return nil, err
		}
		return ytmusic.GenreArt(c, g)
	})
	route(mux, "/api/diag", 0, 40*time.Second, func(c context.Context, r *http.Request) (any, error) {
		id, err := need(r, "id")
		if err != nil {
			return nil, err
		}
		return e.Diag(c, id)
	})
	route(mux, "/api/home", 900, 55*time.Second, func(c context.Context, r *http.Request) (any, error) {
		return e.RandomHome(c)
	})
	route(mux, "/api/spotify", 3600, 25*time.Second, func(c context.Context, r *http.Request) (any, error) {
		id, err := need(r, "id")
		if err != nil {
			return nil, err
		}
		return ytmusic.Spotify(c, r.URL.Query().Get("type"), id)
	})
	mux.HandleFunc("/api/audio", e.AudioHandler)
	mux.HandleFunc("/api/proxy", ytmusic.Proxy)
	mux.HandleFunc("/api/img", ytmusic.ImgProxy)

	// File statis HANYA dari folder public/ (kode Go, go.mod, dan .env tidak ikut tersaji).
	dir := strings.TrimSpace(os.Getenv("STATIC_DIR"))
	if dir == "" {
		dir = "public"
	}
	if _, err := os.Stat(dir + "/index.html"); err != nil {
		log.Fatal("index.html tidak ditemukan di folder '" + dir + "'. Jalankan dari root proyek.")
	}
	mux.Handle("/", noCache(http.FileServer(http.Dir(dir))))

	if os.Getenv("DL_API_KEY") == "" {
		log.Println("PERINGATAN: DL_API_KEY kosong. Pencarian jalan, tapi lagu tidak bisa diputar.")
	}
	port := strings.TrimSpace(os.Getenv("PORT"))
	if port == "" {
		port = "8080"
	}

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		// WriteTimeout sengaja tidak diisi: audio lewat /api/proxy bisa mengalir lama.
	}

	done := make(chan struct{})
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
		close(done)
	}()

	log.Println("Jemci jalan di port " + port)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
	<-done
}
