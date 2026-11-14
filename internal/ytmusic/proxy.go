package ytmusic

// Proxy audio dan gambar. Audio lewat proxy = same-origin, jadi Web Audio (Audio Dinamis/EQ) bisa dipakai
// dan hotlink/Referer converter tidak menghalangi.

import (
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

var pc = &http.Client{Timeout: 0, Transport: &http.Transport{
	Proxy:                 http.ProxyFromEnvironment,
	DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
	ForceAttemptHTTP2:     true,
	MaxIdleConns:          100,
	MaxIdleConnsPerHost:   16,
	IdleConnTimeout:       90 * time.Second,
	TLSHandshakeTimeout:   5 * time.Second,
	ResponseHeaderTimeout: 12 * time.Second,
}}
var ic = &http.Client{Timeout: 15 * time.Second}

// hostPrivat true bila host mengarah ke alamat lokal/privat (mencegah SSRF).
// Hasil pencarian DNS yang berhasil disimpan 5 menit supaya tiap permintaan audio tidak menunggu DNS lagi.
type hpEnt struct {
	v   bool
	exp time.Time
}

var hpC sync.Map

func hostPrivat(h string) bool {
	if v, ok := hpC.Load(h); ok {
		if e := v.(hpEnt); time.Now().Before(e.exp) {
			return e.v
		}
	}
	ips, err := net.LookupIP(h)
	if err != nil || len(ips) == 0 { // gangguan DNS sesaat: coba sekali lagi sebelum menolak
		time.Sleep(150 * time.Millisecond)
		ips, err = net.LookupIP(h)
		if err != nil || len(ips) == 0 {
			return true
		}
	}
	res := false
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
			res = true
		}
	}
	hpC.Store(h, hpEnt{res, time.Now().Add(5 * time.Minute)})
	return res
}

func salin(w http.ResponseWriter, resp *http.Response, u *url.URL) {
	for _, h := range []string{"Content-Length", "Content-Range", "Accept-Ranges", "Cache-Control"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	ct := resp.Header.Get("Content-Type")
	p := strings.ToLower(u.Path)
	if ct == "" || strings.HasPrefix(ct, "application/octet-stream") || strings.HasPrefix(ct, "binary/") {
		switch {
		case strings.HasSuffix(p, ".mp3"):
			ct = "audio/mpeg"
		case strings.HasSuffix(p, ".m4a"), strings.HasSuffix(p, ".mp4"):
			ct = "audio/mp4"
		}
	}
	if ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.WriteHeader(resp.StatusCode)
	// kirim header dan potongan pertama langsung ke browser supaya audio mulai bunyi tanpa menunggu buffer server
	fl, _ := w.(http.Flusher)
	if fl != nil {
		fl.Flush()
	}
	buf := make([]byte, 32*1024)
	first := true
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if first && fl != nil {
				fl.Flush()
				first = false
			}
		}
		if err != nil {
			return
		}
	}
}

// Proxy: GET /api/proxy?url=... (http/https, mendukung Range). Batasi domain dengan env PROXY_ALLOW bila perlu.
func Proxy(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	u, err := url.Parse(r.URL.Query().Get("url"))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || !AllowedHost(u.Hostname()) || hostPrivat(u.Hostname()) {
		http.Error(w, "url tidak diizinkan", http.StatusForbidden)
		return
	}
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, u.String(), nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	if strings.HasSuffix(u.Hostname(), "googlevideo.com") {
		req.Header.Set("User-Agent", "com.google.android.apps.youtube.vr.oculus/1.60.19 (Linux; U; Android 12L; eureka-user Build/SQ3A.220605.009.A1) gzip")
	} else {
		req.Header.Set("Referer", "https://loader.to/")
		req.Header.Set("Origin", "https://loader.to")
	}
	rg := r.Header.Get("Range")
	if rg != "" {
		req.Header.Set("Range", rg)
	}
	resp, err := pc.Do(req)
	if err != nil {
		http.Error(w, "gagal mengambil audio", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	salin(w, resp, u)
}

// ImgProxy: GET /api/img?url=... hanya untuk host gambar YouTube/Google. Dipakai bila gambar langsung gagal.
func ImgProxy(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	u, err := url.Parse(r.URL.Query().Get("url"))
	ok := false
	if err == nil && (u.Scheme == "https" || u.Scheme == "http") {
		for _, s := range []string{"googleusercontent.com", "ggpht.com", "ytimg.com", "youtube.com"} {
			if u.Hostname() == s || strings.HasSuffix(u.Hostname(), "."+s) {
				ok = true
			}
		}
	}
	if !ok {
		http.Error(w, "url tidak diizinkan", http.StatusForbidden)
		return
	}
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, u.String(), nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	req.Header.Set("Referer", "https://music.youtube.com/")
	resp, err := ic.Do(req)
	if err != nil || resp.StatusCode != 200 {
		if resp != nil {
			resp.Body.Close()
		}
		http.Error(w, "gambar tidak tersedia", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Cache-Control", "public, max-age=86400")
	salin(w, resp, u)
}
