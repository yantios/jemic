package ytmusic

// /api/audio?id=VIDEO_ID : audio langsung mengalir ke <audio> tanpa menunggu JSON dan converter.
//
// v3.4 (lagu lebih cepat bunyi):
//   - Jalur langsung YouTube dan converter berlomba BERSAMAAN. Converter ikut jalan setelah 1,2 dtk
//     (atau langsung bila jalur langsung gagal), jadi tidak lagi menunggu jalur langsung gagal dulu.
//   - Permintaan yang sama (prefetch + klik putar) berbagi satu pekerjaan, tidak ada konversi ganda.
//   - Tidak ada lagi "kunci 2 menit" yang memaksa semua lagu lewat converter lambat.
//   - URL hasil disimpan sesuai masa berlaku asli (parameter expire), bukan cuma 20 menit.
//   - /api/audio?id=...&warm=1 : siapkan URL lewat jalur langsung saja (murah).
//     /api/audio?id=...&warm=2 : siapkan dengan converter juga (dipakai untuk lagu berikutnya).

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	uaVR  = "com.google.android.apps.youtube.vr.oculus/1.60.19 (Linux; U; Android 12L; eureka-user Build/SQ3A.220605.009.A1) gzip"
	uaWeb = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

	modeBoth   = "b" // langsung + converter (berlomba)
	modeDirect = "d" // hanya langsung
	modeConv   = "c" // hanya converter
)

type audioEnt struct {
	u   string
	ua  string
	exp time.Time
}

type audioFlight struct {
	done chan struct{}
	ent  audioEnt
	err  error
}

var (
	audioC  sync.Map // id -> audioEnt
	audioMu sync.Mutex
	audioFl = map[string]*audioFlight{}
)

// entExpiry: pakai parameter expire dari URL YouTube (dikurangi 10 menit), maksimal 3 jam.
func entExpiry(u string, def time.Duration) time.Time {
	exp := time.Now().Add(def)
	if pu, err := url.Parse(u); err == nil {
		if v, err := strconv.ParseInt(pu.Query().Get("expire"), 10, 64); err == nil && v > 0 {
			t := time.Unix(v, 0).Add(-10 * time.Minute)
			if lim := time.Now().Add(3 * time.Hour); t.After(lim) {
				t = lim
			}
			if t.After(time.Now().Add(time.Minute)) {
				return t
			}
		}
	}
	return exp
}

// convFast: converter mp3 dan m4a berlomba, yang pertama siap dipakai.
func (y *YTMusicEngine) convFast(ctx context.Context, id string) (*AudioStreamItem, error) {
	c, cancel := context.WithCancel(ctx)
	defer cancel()
	target := "https://music.youtube.com/watch?v=" + id
	type res struct {
		s *AudioStreamItem
		e error
	}
	ch := make(chan res, 2)
	for _, f := range []string{"m4a", "mp3"} {
		go func(f string) {
			s, e := y.resolveDLStream(c, target, f)
			ch <- res{s, e}
		}(f)
	}
	var last error
	for i := 0; i < 2; i++ {
		x := <-ch
		if x.e == nil {
			return x.s, nil
		}
		last = x.e
	}
	return nil, last
}

// pickAudio menjalankan jalur langsung dan converter bersamaan, hasil tercepat dipakai.
func (y *YTMusicEngine) pickAudio(ctx context.Context, id, mode string) (audioEnt, error) {
	c, cancel := context.WithCancel(ctx)
	defer cancel()
	type res struct {
		e    audioEnt
		err  error
		conv bool
	}
	ch := make(chan res, 2)
	n := 0
	directFail := make(chan struct{})

	if mode != modeConv {
		n++
		go func() {
			it, err := y.directStream(c, id)
			if err != nil {
				close(directFail)
				ch <- res{err: err}
				return
			}
			ua := it.UA
			if ua == "" {
				ua = uaVR
			}
			ch <- res{e: audioEnt{u: it.DownloadURL, ua: ua, exp: entExpiry(it.DownloadURL, 20*time.Minute)}}
		}()
	} else {
		close(directFail)
	}
	if mode != modeDirect {
		n++
		go func() {
			if mode == modeBoth {
				select {
				case <-time.After(450 * time.Millisecond):
				case <-directFail:
				case <-c.Done():
					ch <- res{err: c.Err(), conv: true}
					return
				}
			}
			it, err := y.convFast(c, id)
			if err != nil {
				ch <- res{err: err, conv: true}
				return
			}
			ch <- res{e: audioEnt{u: it.DownloadURL, ua: uaWeb, exp: time.Now().Add(20 * time.Minute)}, conv: true}
		}()
	}

	var dErr, cErr error
	for i := 0; i < n; i++ {
		select {
		case x := <-ch:
			if x.err == nil {
				return x.e, nil
			}
			if x.conv {
				cErr = x.err
			} else {
				dErr = x.err
			}
		case <-ctx.Done():
			return audioEnt{}, ctx.Err()
		}
	}
	if cErr != nil {
		return audioEnt{}, cErr
	}
	return audioEnt{}, dErr
}

// getAudio: cache -> pekerjaan yang sedang berjalan (dibagi) -> pekerjaan baru.
func (y *YTMusicEngine) getAudio(ctx context.Context, id, mode string) (audioEnt, error) {
	if v, ok := audioC.Load(id); ok {
		if e := v.(audioEnt); time.Now().Before(e.exp) {
			return e, nil
		}
	}
	key := id + "|" + mode
	audioMu.Lock()
	// klik putar saat prefetch jalur langsung masih berjalan: ikut menunggu hasilnya, jangan mulai ulang.
	if mode == modeBoth {
		if f, ok := audioFl[id+"|"+modeDirect]; ok {
			audioMu.Unlock()
			select {
			case <-f.done:
				if f.err == nil {
					return f.ent, nil
				}
			case <-ctx.Done():
				return audioEnt{}, ctx.Err()
			}
			return y.getAudio(ctx, id, mode)
		}
	}
	if f, ok := audioFl[key]; ok {
		audioMu.Unlock()
		select {
		case <-f.done:
			return f.ent, f.err
		case <-ctx.Done():
			return audioEnt{}, ctx.Err()
		}
	}
	f := &audioFlight{done: make(chan struct{})}
	audioFl[key] = f
	audioMu.Unlock()

	// konteks dipisah dari request: prefetch yang dibatalkan browser tidak menggagalkan peminta lain.
	// Batas lebih pendek mencegah satu sumber macet menahan pemutaran terlalu lama.
	dctx, cancel := context.WithTimeout(context.Background(), 24*time.Second)
	f.ent, f.err = y.pickAudio(dctx, id, mode)
	cancel()
	if f.err == nil {
		audioC.Store(id, f.ent)
	}
	audioMu.Lock()
	delete(audioFl, key)
	audioMu.Unlock()
	close(f.done)
	return f.ent, f.err
}

func (y *YTMusicEngine) upstream(r *http.Request, e audioEnt) (*http.Response, *url.URL) {
	pu, err := url.Parse(e.u)
	// URL ini dibuat server sendiri (googlevideo / converter), jadi PROXY_ALLOW tidak berlaku di sini.
	// Sebelumnya PROXY_ALLOW yang terisi membuat SEMUA audio gagal (HTTP 502 -> "Audio gagal dimuat kode 4").
	if err != nil || (pu.Scheme != "https" && pu.Scheme != "http") || hostPrivat(pu.Hostname()) {
		return nil, nil
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, pu.String(), nil)
	if err != nil {
		return nil, nil
	}
	if strings.HasSuffix(pu.Hostname(), "googlevideo.com") {
		ua := e.ua
		if ua == "" {
			ua = uaVR
		}
		req.Header.Set("User-Agent", ua)
	} else {
		req.Header.Set("User-Agent", uaWeb)
		req.Header.Set("Referer", "https://loader.to/")
		req.Header.Set("Origin", "https://loader.to")
	}
	if rg := r.Header.Get("Range"); rg != "" {
		req.Header.Set("Range", rg)
	}
	resp, err := pc.Do(req)
	if err != nil {
		return nil, nil
	}
	return resp, pu
}

func (y *YTMusicEngine) AudioHandler(w http.ResponseWriter, r *http.Request) {
	id := y.extractVideoID(strings.TrimSpace(r.URL.Query().Get("id")))
	if !reID11.MatchString(id) {
		http.Error(w, "id tidak valid", http.StatusBadRequest)
		return
	}
	if wm := r.URL.Query().Get("warm"); wm != "" {
		mode := modeDirect
		if wm == "2" {
			mode = modeBoth
		}
		y.getAudio(r.Context(), id, mode)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	e, err := y.getAudio(r.Context(), id, modeBoth)
	if err != nil {
		http.Error(w, "gagal menyiapkan audio: "+err.Error(), http.StatusBadGateway)
		return
	}
	resp, pu := y.upstream(r, e)
	if resp == nil || resp.StatusCode >= 400 {
		if resp != nil {
			resp.Body.Close()
		}
		// URL lama mati (kedaluwarsa / terikat IP): ambil ulang lewat converter saja
		audioC.Delete(id)
		e, err = y.getAudio(r.Context(), id, modeConv)
		if err != nil {
			http.Error(w, "gagal menyiapkan audio", http.StatusBadGateway)
			return
		}
		resp, pu = y.upstream(r, e)
		if resp == nil || resp.StatusCode >= 400 {
			if resp != nil {
				resp.Body.Close()
			}
			audioC.Delete(id)
			http.Error(w, "gagal mengambil audio", http.StatusBadGateway)
			return
		}
	}
	defer resp.Body.Close()
	w.Header().Set("Access-Control-Allow-Origin", "*")
	salin(w, resp, pu)
}

// Diag: /api/diag?id=VIDEO_ID memeriksa tiap jalur audio dan mengembalikan penyebab kegagalan yang jelas
// (key converter kosong, YouTube menolak, dsb). Dipakai UI saat audio gagal dimuat.
func (y *YTMusicEngine) Diag(ctx context.Context, rawID string) (any, error) {
	id := y.extractVideoID(strings.TrimSpace(rawID))
	if !reID11.MatchString(id) {
		return nil, BadReq("id tidak valid")
	}
	out := map[string]any{"video_id": id, "dl_api_key_set": y.dlAPIKey != ""}
	clients := map[string]string{}
	var okURL, okUA string
	for _, cl := range directClients {
		it, err := y.directWith(ctx, id, cl)
		name := cl.name + " " + cl.version
		if err != nil {
			clients[name] = err.Error()
			continue
		}
		clients[name] = "ok"
		if okURL == "" {
			okURL, okUA = it.DownloadURL, cl.ua
		}
	}
	out["direct_clients"] = clients
	if okURL == "" {
		out["direct_probe_status"] = 0
		return out, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, okURL, nil)
	if err != nil {
		out["direct_probe_status"] = -1
		return out, nil
	}
	req.Header.Set("User-Agent", okUA)
	req.Header.Set("Range", "bytes=0-1")
	resp, err := pc.Do(req)
	if err != nil {
		out["direct_probe_status"] = -1
		out["direct_probe_error"] = err.Error()
		return out, nil
	}
	resp.Body.Close()
	out["direct_probe_status"] = resp.StatusCode
	return out, nil
}
