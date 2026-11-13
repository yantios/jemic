package ytmusic

// /api/video?id=VIDEO_ID : klip video MP4 langsung (tanpa embed YouTube, jadi tidak ada UI YouTube sama sekali).
//
// Cara kerja:
//   - Semua klien YouTube (ANDROID_VR, IOS, TV embed) dicoba BERSAMAAN, yang pertama berhasil dipakai.
//   - Format pilihan: MP4 muxed (itag 18/22, video+audio dalam satu berkas) supaya Range/seek cepat dan
//     browser tidak perlu menyambung dua aliran. Cadangan: MP4 video-saja (avc1) maksimal 720p.
//   - Video diputar tanpa suara di <video>; suara tetap dari <audio> Jemci, jadi tidak ada jeda audio.
//   - Hasil disimpan sesuai masa berlaku URL, dan permintaan yang sama berbagi satu pekerjaan.
//   - /api/video?id=...&warm=1 : siapkan URL saja (tanpa mengalirkan data).
//   - /api/video?id=...&info=1 : JSON ringan (lebar, tinggi, muxed) untuk UI.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/tidwall/gjson"
)

type videoEnt struct {
	u      string
	ua     string
	exp    time.Time
	w, h   int
	muxed  bool
	client string
}

type videoFlight struct {
	done chan struct{}
	ent  videoEnt
	err  error
}

var (
	videoC  sync.Map // id -> videoEnt
	videoMu sync.Mutex
	videoFl = map[string]*videoFlight{}
)

// pickVideoFormat memilih format terbaik dari respons /player.
func pickVideoFormat(res gjson.Result) (u string, w, h int, muxed bool) {
	const maxH = 720
	// 1) muxed mp4 (video+audio). Pilih yang tinggi-nya paling besar tapi <= 720.
	bestH := 0
	for _, f := range res.Get("streamingData.formats").Array() {
		mt := f.Get("mimeType").String()
		if !strings.HasPrefix(mt, "video/mp4") {
			continue
		}
		fu := f.Get("url").String()
		if fu == "" {
			continue
		}
		fh := int(f.Get("height").Int())
		if fh <= maxH && fh >= bestH {
			bestH, u, w, h, muxed = fh, fu, int(f.Get("width").Int()), fh, true
		}
	}
	if u != "" {
		return
	}
	// 2) video-saja mp4 (avc1), <= 720p. Bitrate terendah di tinggi yang sama supaya ringan.
	bestH = 0
	var bestBr int64 = 1 << 60
	for _, f := range res.Get("streamingData.adaptiveFormats").Array() {
		mt := f.Get("mimeType").String()
		if !strings.HasPrefix(mt, "video/mp4") || !strings.Contains(mt, "avc1") {
			continue
		}
		fu := f.Get("url").String()
		if fu == "" {
			continue
		}
		fh := int(f.Get("height").Int())
		if fh > maxH || fh < 240 {
			continue
		}
		br := f.Get("bitrate").Int()
		if fh > bestH || (fh == bestH && br < bestBr) {
			bestH, bestBr = fh, br
			u, w, h, muxed = fu, int(f.Get("width").Int()), fh, false
		}
	}
	return
}

func (y *YTMusicEngine) videoWith(ctx context.Context, videoID string, c ytClient) (videoEnt, error) {
	client := map[string]any{"clientName": c.name, "clientVersion": c.version, "hl": "en", "gl": "US"}
	for k, v := range c.extra {
		client[k] = v
	}
	cx := map[string]any{"client": client}
	if c.embed {
		cx["thirdParty"] = map[string]any{"embedUrl": "https://www.youtube.com/"}
	}
	b, err := json.Marshal(map[string]any{"videoId": videoID, "contentCheckOk": true, "racyCheckOk": true, "context": cx})
	if err != nil {
		return videoEnt{}, err
	}
	dctx, cancel := context.WithTimeout(ctx, 7*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(dctx, http.MethodPost, "https://www.youtube.com/youtubei/v1/player?prettyPrint=false", bytes.NewReader(b))
	if err != nil {
		return videoEnt{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", c.ua)
	req.Header.Set("X-YouTube-Client-Name", c.id)
	req.Header.Set("X-YouTube-Client-Version", c.version)
	resp, err := y.client.Do(req)
	if err != nil {
		return videoEnt{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return videoEnt{}, err
	}
	res := gjson.ParseBytes(body)
	if st := res.Get("playabilityStatus.status").String(); st != "OK" {
		return videoEnt{}, fmt.Errorf("%s:%s", c.name, st)
	}
	u, w, h, muxed := pickVideoFormat(res)
	if u == "" {
		return videoEnt{}, fmt.Errorf("%s:no_video", c.name)
	}
	return videoEnt{u: u, ua: c.ua, w: w, h: h, muxed: muxed, client: c.name, exp: entExpiry(u, 20*time.Minute)}, nil
}

func (y *YTMusicEngine) pickVideo(ctx context.Context, id string) (videoEnt, error) {
	c, cancel := context.WithCancel(ctx)
	defer cancel()
	type vres struct {
		e   videoEnt
		err error
	}
	ch := make(chan vres, len(directClients))
	for _, cl := range directClients {
		go func(cl ytClient) {
			e, err := y.videoWith(c, id, cl)
			ch <- vres{e, err}
		}(cl)
	}
	var errs []string
	for range directClients {
		x := <-ch
		if x.err == nil {
			return x.e, nil
		}
		errs = append(errs, x.err.Error())
	}
	return videoEnt{}, fmt.Errorf("video_unavailable: %s", strings.Join(errs, " | "))
}

func (y *YTMusicEngine) getVideo(ctx context.Context, id string, fresh bool) (videoEnt, error) {
	if !fresh {
		if v, ok := videoC.Load(id); ok {
			if e := v.(videoEnt); time.Now().Before(e.exp) {
				return e, nil
			}
		}
	}
	videoMu.Lock()
	if f, ok := videoFl[id]; ok {
		videoMu.Unlock()
		select {
		case <-f.done:
			return f.ent, f.err
		case <-ctx.Done():
			return videoEnt{}, ctx.Err()
		}
	}
	f := &videoFlight{done: make(chan struct{})}
	videoFl[id] = f
	videoMu.Unlock()

	dctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	f.ent, f.err = y.pickVideo(dctx, id)
	cancel()
	if f.err == nil {
		videoC.Store(id, f.ent)
	}
	videoMu.Lock()
	delete(videoFl, id)
	videoMu.Unlock()
	close(f.done)
	return f.ent, f.err
}

// VideoHandler: mengalirkan MP4 dengan dukungan Range (seek instan) lewat server sendiri.
func (y *YTMusicEngine) VideoHandler(w http.ResponseWriter, r *http.Request) {
	id := y.extractVideoID(strings.TrimSpace(r.URL.Query().Get("id")))
	if !reID11.MatchString(id) {
		http.Error(w, "id tidak valid", http.StatusBadRequest)
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
	q := r.URL.Query()
	if q.Get("warm") != "" {
		y.getVideo(r.Context(), id, false)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if q.Get("info") != "" {
		e, err := y.getVideo(r.Context(), id, false)
		if err != nil {
			http.Error(w, "video tidak tersedia", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(map[string]any{"width": e.w, "height": e.h, "muxed": e.muxed, "client": e.client})
		return
	}
	e, err := y.getVideo(r.Context(), id, false)
	if err != nil {
		http.Error(w, "video tidak tersedia: "+err.Error(), http.StatusBadGateway)
		return
	}
	resp, pu := y.videoUpstream(r, e)
	if resp == nil || resp.StatusCode >= 400 {
		if resp != nil {
			resp.Body.Close()
		}
		// URL lama mati (kedaluwarsa / terikat IP): ambil ulang sekali
		videoC.Delete(id)
		e, err = y.getVideo(r.Context(), id, true)
		if err != nil {
			http.Error(w, "video tidak tersedia", http.StatusBadGateway)
			return
		}
		resp, pu = y.videoUpstream(r, e)
		if resp == nil || resp.StatusCode >= 400 {
			if resp != nil {
				resp.Body.Close()
			}
			videoC.Delete(id)
			http.Error(w, "gagal mengambil video", http.StatusBadGateway)
			return
		}
	}
	defer resp.Body.Close()
	// pastikan tipe benar supaya <video> mau memutar
	if ct := resp.Header.Get("Content-Type"); ct == "" || strings.HasPrefix(ct, "application/octet-stream") {
		resp.Header.Set("Content-Type", "video/mp4")
	}
	salin(w, resp, pu)
}

func (y *YTMusicEngine) videoUpstream(r *http.Request, e videoEnt) (*http.Response, *url.URL) {
	pu, err := url.Parse(e.u)
	if err != nil || (pu.Scheme != "https" && pu.Scheme != "http") || hostPrivat(pu.Hostname()) {
		return nil, nil
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, pu.String(), nil)
	if err != nil {
		return nil, nil
	}
	ua := e.ua
	if ua == "" {
		ua = uaVR
	}
	req.Header.Set("User-Agent", ua)
	if rg := r.Header.Get("Range"); rg != "" {
		req.Header.Set("Range", rg)
	} else {
		// tanpa Range googlevideo bisa menahan/melambatkan; minta dari awal supaya dibalas 206 yang bisa di-seek
		req.Header.Set("Range", "bytes=0-")
	}
	resp, err := pc.Do(req)
	if err != nil {
		return nil, nil
	}
	return resp, pu
}
