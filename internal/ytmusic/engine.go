package ytmusic

// Logic dari ytmusic.go milik Jelan, dipindah apa adanya ke package.
// Perubahan: main() CLI dihapus, API key/domain dari env, TLS normal (tanpa InsecureSkipVerify),
// Play dipecah jadi PlayMeta (cepat) dan Stream (lambat) supaya UI tidak menunggu.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/tidwall/gjson"
)

type ExtractionResult struct {
	Success    bool         `json:"success"`
	StatusCode int          `json:"status_code,omitempty"`
	Data       any          `json:"data,omitempty"`
	Error      *ErrorDetail `json:"error,omitempty"`
	Timestamp  int64        `json:"timestamp"`
}

type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ThumbnailItem struct {
	URL    string `json:"url"`
	Width  int64  `json:"width,omitempty"`
	Height int64  `json:"height,omitempty"`
}

type TrackItem struct {
	VideoID    string          `json:"video_id,omitempty"`
	Title      string          `json:"title"`
	Artist     string          `json:"artist,omitempty"`
	Album      string          `json:"album,omitempty"`
	Duration   string          `json:"duration,omitempty"`
	Thumbnails []ThumbnailItem `json:"thumbnails,omitempty"`
	URL        string          `json:"url,omitempty"`
}

type AlbumItem struct {
	BrowseID   string          `json:"browse_id,omitempty"`
	PlaylistID string          `json:"playlist_id,omitempty"`
	Title      string          `json:"title"`
	Artist     string          `json:"artist,omitempty"`
	Year       string          `json:"year,omitempty"`
	Thumbnails []ThumbnailItem `json:"thumbnails,omitempty"`
	URL        string          `json:"url,omitempty"`
}

type ArtistItem struct {
	BrowseID    string          `json:"browse_id,omitempty"`
	Name        string          `json:"name"`
	Subscribers string          `json:"subscribers,omitempty"`
	Thumbnails  []ThumbnailItem `json:"thumbnails,omitempty"`
	URL         string          `json:"url,omitempty"`
}

type PlaylistItem struct {
	BrowseID   string          `json:"browse_id,omitempty"`
	PlaylistID string          `json:"playlist_id,omitempty"`
	Title      string          `json:"title"`
	Author     string          `json:"author,omitempty"`
	ItemCount  string          `json:"item_count,omitempty"`
	Thumbnails []ThumbnailItem `json:"thumbnails,omitempty"`
	URL        string          `json:"url,omitempty"`
}

type SearchResult struct {
	Query     string         `json:"query"`
	TopResult any            `json:"top_result,omitempty"`
	Songs     []TrackItem    `json:"songs,omitempty"`
	Albums    []AlbumItem    `json:"albums,omitempty"`
	Artists   []ArtistItem   `json:"artists,omitempty"`
	Playlists []PlaylistItem `json:"playlists,omitempty"`
}

type AudioStreamItem struct {
	Format      string `json:"format"`
	Quality     string `json:"quality"`
	FileType    string `json:"file_type"`
	DownloadURL string `json:"download_url"`
	Status      string `json:"status"`
	Direct      bool   `json:"direct,omitempty"`
	UA          string `json:"-"`
}

type PlayResult struct {
	VideoID      string            `json:"video_id"`
	Title        string            `json:"title"`
	Artist       string            `json:"artist,omitempty"`
	Album        string            `json:"album,omitempty"`
	Duration     string            `json:"duration,omitempty"`
	Thumbnails   []ThumbnailItem   `json:"thumbnails,omitempty"`
	CanonicalURL string            `json:"canonical_url"`
	Lyrics       string            `json:"lyrics,omitempty"`
	Streams      []AudioStreamItem `json:"streams,omitempty"`
	UpNext       []TrackItem       `json:"up_next,omitempty"`
	MVID         string            `json:"mv_id,omitempty"`
}

type DownloadResult struct {
	VideoID      string            `json:"video_id"`
	Title        string            `json:"title"`
	CanonicalURL string            `json:"canonical_url"`
	Thumbnails   []ThumbnailItem   `json:"thumbnails,omitempty"`
	Streams      []AudioStreamItem `json:"streams"`
}

type AlbumResult struct {
	BrowseID   string          `json:"browse_id"`
	Title      string          `json:"title"`
	Artist     string          `json:"artist,omitempty"`
	Year       string          `json:"year,omitempty"`
	TrackCount int             `json:"track_count"`
	Thumbnails []ThumbnailItem `json:"thumbnails,omitempty"`
	Tracks     []TrackItem     `json:"tracks"`
}

type PlaylistResult struct {
	PlaylistID string          `json:"playlist_id"`
	Title      string          `json:"title"`
	Author     string          `json:"author,omitempty"`
	TrackCount int             `json:"track_count"`
	Thumbnails []ThumbnailItem `json:"thumbnails,omitempty"`
	Tracks     []TrackItem     `json:"tracks"`
}

type YTMusicEngine struct {
	client        *http.Client
	dlAPIKey      string
	primaryDLDom  string
	fallbackDLDom string
}

var (
	reDur   = regexp.MustCompile(`^\d+:\d+`)
	reYear  = regexp.MustCompile(`^\d{4}$`)
	reID11  = regexp.MustCompile(`^[a-zA-Z0-9_-]{11}$`)
	reV     = regexp.MustCompile(`[?&]v=([a-zA-Z0-9_-]{11})`)
	reShort = regexp.MustCompile(`youtu\.be/([a-zA-Z0-9_-]{11})`)
	reList  = regexp.MustCompile(`[?&]list=([a-zA-Z0-9_-]+)`)
	reAlbum = regexp.MustCompile(`/browse/(MPREb[a-zA-Z0-9_-]+)`)
	reOLAK  = regexp.MustCompile(`[?&]list=(OLAK5uy_[a-zA-Z0-9_-]+)`)
)

func getenv(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func NewYTMusicEngine(timeout time.Duration) (*YTMusicEngine, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	// Catatan: InsecureSkipVerify dihapus. Dengan false, sertifikat HTTPS diverifikasi
	// sehingga koneksi tidak bisa disadap/dipalsukan (man-in-the-middle).
	transport := &http.Transport{
		Proxy:              http.ProxyFromEnvironment,
		MaxIdleConns:       100,
		IdleConnTimeout:    90 * time.Second,
		DisableCompression: false,
		ForceAttemptHTTP2:  true,
	}
	client := &http.Client{Jar: jar, Transport: transport, Timeout: timeout}
	return &YTMusicEngine{
		client:        client,
		dlAPIKey:      os.Getenv("DL_API_KEY"),
		primaryDLDom:  getenv("DL_PRIMARY", "p.savenow.to"),
		fallbackDLDom: getenv("DL_FALLBACK", "p.lbserver.xyz"),
	}, nil
}

func (y *YTMusicEngine) applyHeaders(req *http.Request) {
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "id-ID,id;q=0.9,en-US;q=0.8,en;q=0.7")
	req.Header.Set("Origin", "https://music.youtube.com")
	req.Header.Set("Referer", "https://music.youtube.com/")
	req.Header.Set("Sec-Ch-Ua", `"Google Chrome";v="131", "Chromium";v="131", "Not_A Brand";v="24"`)
	req.Header.Set("Sec-Ch-Ua-Mobile", "?0")
	req.Header.Set("Sec-Ch-Ua-Platform", `"Windows"`)
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
}

func (y *YTMusicEngine) postInnerTube(ctx context.Context, endpoint string, payload map[string]any) (gjson.Result, error) {
	fullURL := "https://music.youtube.com/youtubei/v1/" + strings.TrimPrefix(endpoint, "/")
	if _, ok := payload["context"]; !ok {
		payload["context"] = map[string]any{
			"client": map[string]any{
				"clientName":    "WEB_REMIX",
				"clientVersion": "1.20241001.01.00",
				"hl":            getenv("YT_HL", "en"),
				"gl":            getenv("YT_GL", "US"),
			},
		}
	}
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return gjson.Result{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fullURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return gjson.Result{}, err
	}
	y.applyHeaders(req)
	req.Header.Set("Content-Type", "application/json")
	resp, err := y.client.Do(req)
	if err != nil {
		return gjson.Result{}, err
	}
	defer resp.Body.Close()
	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return gjson.Result{}, err
	}
	return gjson.ParseBytes(respBytes), nil
}

func (y *YTMusicEngine) parseThumbnails(raw gjson.Result) []ThumbnailItem {
	var list []ThumbnailItem
	for _, item := range raw.Array() {
		u := item.Get("url").String()
		if u != "" {
			list = append(list, ThumbnailItem{URL: u, Width: item.Get("width").Int(), Height: item.Get("height").Int()})
		}
	}
	return list
}

func (y *YTMusicEngine) Search(ctx context.Context, query string) (*SearchResult, error) {
	res, err := y.postInnerTube(ctx, "search", map[string]any{"query": query})
	if err != nil {
		return nil, err
	}
	result := &SearchResult{Query: query}

	sections := res.Get("contents.tabbedSearchResultsRenderer.tabs.0.tabRenderer.content.sectionListRenderer.contents").Array()
	for _, sec := range sections {
		if card := sec.Get("musicCardShelfRenderer"); card.Exists() {
			title := card.Get("title.runs.0.text").String()
			videoID := card.Get("title.runs.0.navigationEndpoint.watchEndpoint.videoId").String()
			var subTexts []string
			for _, st := range card.Get("subtitle.runs").Array() {
				t := strings.TrimSpace(st.Get("text").String())
				if t != "" && t != "•" {
					subTexts = append(subTexts, t)
				}
			}
			result.TopResult = map[string]any{
				"title":      title,
				"video_id":   videoID,
				"url":        "https://music.youtube.com/watch?v=" + videoID,
				"subtitles":  subTexts,
				"thumbnails": y.parseThumbnails(card.Get("thumbnail.musicThumbnailRenderer.thumbnail.thumbnails")),
			}
		}

		items := sec.Get("itemSectionRenderer.contents").Array()
		if len(items) == 0 {
			items = sec.Get("musicShelfRenderer.contents").Array()
		}
		for _, item := range items {
			renderer := item.Get("musicResponsiveListItemRenderer")
			if !renderer.Exists() {
				continue
			}
			title := renderer.Get("flexColumns.0.musicResponsiveListItemFlexColumnRenderer.text.runs.0.text").String()
			navEndpoint := renderer.Get("flexColumns.0.musicResponsiveListItemFlexColumnRenderer.text.runs.0.navigationEndpoint")
			watchVideoID := navEndpoint.Get("watchEndpoint.videoId").String()
			if watchVideoID == "" {
				watchVideoID = renderer.Get("playlistItemData.videoId").String()
			}
			browseID := navEndpoint.Get("browseEndpoint.browseId").String()

			var subParts []string
			for _, r := range renderer.Get("flexColumns.1.musicResponsiveListItemFlexColumnRenderer.text.runs").Array() {
				t := strings.TrimSpace(r.Get("text").String())
				if t != "" && t != "•" {
					subParts = append(subParts, t)
				}
			}
			itemType, artistName, durationStr := "", "", ""
			if len(subParts) > 0 {
				itemType = strings.ToLower(subParts[0])
			}
			if len(subParts) > 1 {
				artistName = subParts[1]
			}
			if len(subParts) > 2 && reDur.MatchString(subParts[len(subParts)-1]) {
				durationStr = subParts[len(subParts)-1]
			}
			thumbs := y.parseThumbnails(renderer.Get("thumbnail.musicThumbnailRenderer.thumbnail.thumbnails"))

			if watchVideoID != "" || itemType == "lagu" || itemType == "song" || itemType == "video" {
				result.Songs = append(result.Songs, TrackItem{
					VideoID: watchVideoID, Title: title, Artist: artistName, Duration: durationStr,
					Thumbnails: thumbs, URL: "https://music.youtube.com/watch?v=" + watchVideoID,
				})
			} else if strings.HasPrefix(browseID, "MPREb") || itemType == "album" || itemType == "ep" || itemType == "single" {
				yearStr := ""
				if len(subParts) > 2 {
					yearStr = subParts[2]
				}
				result.Albums = append(result.Albums, AlbumItem{
					BrowseID: browseID, Title: title, Artist: artistName, Year: yearStr,
					Thumbnails: thumbs, URL: "https://music.youtube.com/browse/" + browseID,
				})
			} else if itemType == "artis" || itemType == "artist" {
				subs := ""
				if len(subParts) > 1 {
					subs = subParts[1]
				}
				result.Artists = append(result.Artists, ArtistItem{
					BrowseID: browseID, Name: title, Subscribers: subs,
					Thumbnails: thumbs, URL: "https://music.youtube.com/browse/" + browseID,
				})
			} else if strings.HasPrefix(browseID, "VL") || itemType == "playlist" || itemType == "daftar putar" {
				result.Playlists = append(result.Playlists, PlaylistItem{
					BrowseID: browseID, PlaylistID: strings.TrimPrefix(browseID, "VL"), Title: title, Author: artistName,
					Thumbnails: thumbs, URL: "https://music.youtube.com/playlist?list=" + strings.TrimPrefix(browseID, "VL"),
				})
			}
		}
	}
	return result, nil
}

// initDL memulai konversi di server converter. Domain utama dicoba dulu; bila belum menjawab
// dalam 2 detik (atau gagal), domain cadangan ikut dicoba bersamaan. Yang berhasil duluan dipakai.
// Ini memangkas waktu tunggu saat domain utama lambat (sebelumnya menunggu sampai 8 detik baru pindah).
func (y *YTMusicEngine) initDL(ctx context.Context, q url.Values) (gjson.Result, string, error) {
	type out struct {
		res gjson.Result
		dom string
		err error
	}
	try := func(c context.Context, d string) out {
		ep := fmt.Sprintf("https://%s/api/v2/download?%s", d, q.Encode())
		ictx, icancel := context.WithTimeout(c, 8*time.Second)
		defer icancel()
		req, err := http.NewRequestWithContext(ictx, http.MethodGet, ep, nil)
		if err != nil {
			return out{err: err, dom: d}
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
		req.Header.Set("Origin", "https://loader.to")
		req.Header.Set("Referer", "https://loader.to/")
		resp, err := y.client.Do(req)
		if err != nil {
			return out{err: fmt.Errorf("%s: %v", d, err), dom: d}
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		res := gjson.ParseBytes(b)
		if res.Get("success").Bool() || res.Get("progress_url").String() != "" {
			return out{res: res, dom: d}
		}
		// simpan alasan penolakan dari layanan (mis. API key salah / kuota habis)
		msg := strings.TrimSpace(res.Get("message").String() + " " + res.Get("error").String() + " " + res.Get("text").String())
		if msg == "" {
			msg = fmt.Sprintf("HTTP %d: %.120s", resp.StatusCode, strings.TrimSpace(string(b)))
		}
		return out{err: fmt.Errorf("%s: %s", d, msg), dom: d}
	}
	c, cancel := context.WithCancel(ctx)
	defer cancel()
	doms := []string{y.primaryDLDom}
	if y.fallbackDLDom != "" && y.fallbackDLDom != y.primaryDLDom {
		doms = append(doms, y.fallbackDLDom)
	}
	ch := make(chan out, len(doms))
	go func() { ch <- try(c, doms[0]) }()
	started, got := 1, 0
	var last error
	var hedge <-chan time.Time
	if len(doms) > 1 {
		hedge = time.After(700 * time.Millisecond)
	}
	for got < len(doms) {
		select {
		case <-hedge:
			hedge = nil
			if started < len(doms) {
				started++
				go func() { ch <- try(c, doms[1]) }()
			}
		case r := <-ch:
			got++
			if r.err == nil {
				return r.res, r.dom, nil
			}
			last = r.err
			if started < len(doms) { // domain utama gagal: langsung coba cadangan
				started++
				go func() { ch <- try(c, doms[1]) }()
			}
		case <-ctx.Done():
			return gjson.Result{}, "", ctx.Err()
		}
	}
	return gjson.Result{}, "", last
}

func (y *YTMusicEngine) resolveDLStream(ctx context.Context, targetURL string, format string) (*AudioStreamItem, error) {
	if y.dlAPIKey == "" {
		return nil, fmt.Errorf("DL_API_KEY belum diset di environment variable")
	}
	formatQuality := "320kbps High Quality Audio"
	fileType := "mp3"
	if strings.ToLower(format) == "m4a" {
		formatQuality = "AAC M4A Audio"
		fileType = "m4a"
	} else if strings.ToLower(format) == "flac" {
		formatQuality = "Lossless Audio"
		fileType = "flac"
	} else if strings.ToLower(format) == "wav" {
		formatQuality = "Uncompressed Wave Audio"
		fileType = "wav"
	}

	q := url.Values{}
	q.Set("format", format)
	q.Set("url", targetURL)
	q.Set("apikey", y.dlAPIKey)
	if strings.ToLower(format) == "mp3" {
		q.Set("audio_quality", "320") // minta kualitas tertinggi; diabaikan jika layanan tidak mendukung
	}

	initRes, okDom, err := y.initDL(ctx, q)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "api key") {
			return nil, BadReq("API key converter tidak valid. Ambil key di video-download-api.com/dashboard, isi DL_API_KEY (Railway > Variables), lalu redeploy.")
		}
		return nil, fmt.Errorf("dl_service_init_failed: %v", err)
	}

	progressURL := initRes.Get("progress_url").String()
	if progressURL == "" {
		progressURL = fmt.Sprintf("https://%s/api/v2/progress?id=%s", okDom, initRes.Get("id").String())
	}

	for i := 0; i < 160; i++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		pctx, pcancel := context.WithTimeout(ctx, 6*time.Second)
		req, err := http.NewRequestWithContext(pctx, http.MethodGet, progressURL, nil)
		if err == nil {
			req.Header.Set("User-Agent", "Mozilla/5.0")
			req.Header.Set("Origin", "https://loader.to")
			req.Header.Set("Referer", "https://loader.to/")
			resp, err := y.client.Do(req)
			if err == nil {
				b, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				pRes := gjson.ParseBytes(b)
				dlURL := pRes.Get("download_url").String()
				if pRes.Get("success").Int() == 1 && dlURL != "" {
					pcancel()
					return &AudioStreamItem{Format: format, Quality: formatQuality, FileType: fileType, DownloadURL: dlURL, Status: "Available"}, nil
				}
			}
		}
		pcancel()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}
	return nil, fmt.Errorf("dl_stream_generation_timeout")
}

// PlayMeta = bagian cepat dari Play: metadata, thumbnail, lirik teks, up_next. TANPA menunggu stream.
func (y *YTMusicEngine) PlayMeta(ctx context.Context, videoID string) (*PlayResult, error) {
	cleanID := y.extractVideoID(videoID)
	if cleanID == "" {
		return nil, BadReq("invalid_video_id: " + videoID)
	}
	nextRes, err := y.postInnerTube(ctx, "next", map[string]any{"videoId": cleanID, "playlistId": "RDAMVM" + cleanID, "isAudioOnly": true})
	if err != nil || !nextRes.Get("contents.singleColumnMusicWatchNextResultsRenderer").Exists() {
		nextRes, err = y.postInnerTube(ctx, "next", map[string]any{"videoId": cleanID})
		if err != nil {
			return nil, err
		}
	}

	tabs := nextRes.Get("contents.singleColumnMusicWatchNextResultsRenderer.tabbedRenderer.watchNextTabbedResultsRenderer.tabs").Array()
	var currentQueueItem gjson.Result
	var upNextList []TrackItem
	var lyricsBrowseID string
	var mvID string

	for _, tab := range tabs {
		tabRenderer := tab.Get("tabRenderer")
		endpointID := tabRenderer.Get("endpoint.browseEndpoint.browseId").String()
		if strings.HasPrefix(endpointID, "MPLY") {
			lyricsBrowseID = endpointID
		}
		queueContents := tabRenderer.Get("content.musicQueueRenderer.content.playlistPanelRenderer.contents").Array()
		if len(queueContents) > 0 {
			first := queueContents[0]
			currentQueueItem = first.Get("playlistPanelVideoRenderer")
			if !currentQueueItem.Exists() {
				currentQueueItem = first.Get("playlistPanelVideoWrapperRenderer.primaryRenderer.playlistPanelVideoRenderer")
			}
			counter := first.Get("playlistPanelVideoWrapperRenderer.counterpart.0.counterpartRenderer.playlistPanelVideoRenderer")
			mvID = pickMV(currentQueueItem, counter)
			for i := 1; i < len(queueContents) && i <= 40; i++ {
				qv := queueContents[i].Get("playlistPanelVideoRenderer")
				if !qv.Exists() {
					qv = queueContents[i].Get("playlistPanelVideoWrapperRenderer.primaryRenderer.playlistPanelVideoRenderer")
				}
				if qv.Exists() {
					upNextList = append(upNextList, TrackItem{
						VideoID:    qv.Get("videoId").String(),
						Title:      qv.Get("title.runs.0.text").String(),
						Artist:     qv.Get("longBylineText.runs.0.text").String(),
						Duration:   qv.Get("lengthText.runs.0.text").String(),
						Thumbnails: y.parseThumbnails(qv.Get("thumbnail.thumbnails")),
						URL:        "https://music.youtube.com/watch?v=" + qv.Get("videoId").String(),
					})
				}
			}
		}
	}

	title := currentQueueItem.Get("title.runs.0.text").String()
	artist := currentQueueItem.Get("longBylineText.runs.0.text").String()
	album := currentQueueItem.Get("longBylineText.runs.2.text").String()
	duration := currentQueueItem.Get("lengthText.runs.0.text").String()
	thumbnails := y.parseThumbnails(currentQueueItem.Get("thumbnail.thumbnails"))
	if len(thumbnails) == 0 {
		thumbnails = append(thumbnails, ThumbnailItem{URL: fmt.Sprintf("https://i.ytimg.com/vi/%s/hqdefault.jpg", cleanID)})
	}

	lyricsText := ""
	if lyricsBrowseID != "" {
		lyrRes, err := y.postInnerTube(ctx, "browse", map[string]any{"browseId": lyricsBrowseID})
		if err == nil {
			lyricsText = lyrRes.Get("contents.sectionListRenderer.contents.0.musicDescriptionShelfRenderer.description.runs.0.text").String()
		}
	}

	return &PlayResult{
		VideoID: cleanID, Title: title, Artist: artist, Album: album, Duration: duration,
		Thumbnails: thumbnails, CanonicalURL: "https://music.youtube.com/watch?v=" + cleanID,
		Lyrics: lyricsText, UpNext: upNextList, MVID: mvID,
	}, nil
}

// mvType = jenis video YouTube Music: ATV (audio saja), OMV (klip resmi), UGC, dst.
func mvType(r gjson.Result) string {
	return r.Get("navigationEndpoint.watchEndpoint.watchEndpointMusicSupportedConfigs.watchEndpointMusicConfig.musicVideoType").String()
}

// pickMV memilih video id yang berisi gambar bergerak (klip), bukan sekadar audio + sampul.
// p = item utama antrean, c = pasangan (counterpart). Kosong bila tidak ada klip.
func pickMV(p, c gjson.Result) string {
	for _, r := range []gjson.Result{p, c} {
		if r.Exists() && mvType(r) != "MUSIC_VIDEO_TYPE_ATV" {
			if id := r.Get("videoId").String(); id != "" {
				return id
			}
		}
	}
	return ""
}

// VideoSearch mencari video (klip, live, dll) di YouTube Music. Dipakai sebagai cadangan
// kalau lagu tidak punya klip resmi di data "next".
func (y *YTMusicEngine) VideoSearch(ctx context.Context, query string) ([]TrackItem, error) {
	res, err := y.postInnerTube(ctx, "search", map[string]any{"query": query, "params": "EgWKAQIQAWoMEA4QChADEAQQCRAF"})
	if err != nil {
		return nil, err
	}
	var out []TrackItem
	sections := res.Get("contents.tabbedSearchResultsRenderer.tabs.0.tabRenderer.content.sectionListRenderer.contents").Array()
	for _, sec := range sections {
		for _, item := range sec.Get("musicShelfRenderer.contents").Array() {
			r := item.Get("musicResponsiveListItemRenderer")
			if !r.Exists() {
				continue
			}
			id := r.Get("playlistItemData.videoId").String()
			if id == "" {
				id = r.Get("flexColumns.0.musicResponsiveListItemFlexColumnRenderer.text.runs.0.navigationEndpoint.watchEndpoint.videoId").String()
			}
			if id == "" {
				continue
			}
			var parts []string
			for _, rn := range r.Get("flexColumns.1.musicResponsiveListItemFlexColumnRenderer.text.runs").Array() {
				t := strings.TrimSpace(rn.Get("text").String())
				if t != "" && t != "•" {
					parts = append(parts, t)
				}
			}
			artist := ""
			if len(parts) > 1 {
				artist = parts[1]
			}
			out = append(out, TrackItem{
				VideoID: id,
				Title:   r.Get("flexColumns.0.musicResponsiveListItemFlexColumnRenderer.text.runs.0.text").String(),
				Artist:  artist,
			})
			if len(out) >= 6 {
				return out, nil
			}
		}
	}
	return out, nil
}

// Stream = bagian lambat. format: auto (HD, utamakan mp3 320, tunggu MP3 maks 0,6 dtk setelah m4a siap), fast (yang tercepat), mp3, m4a.
func (y *YTMusicEngine) Stream(ctx context.Context, videoID, format string) (*AudioStreamItem, error) {
	cleanID := y.extractVideoID(videoID)
	if !reID11.MatchString(cleanID) {
		return nil, BadReq("invalid_video_id: " + videoID)
	}
	f := strings.ToLower(format)
	if f == "" {
		f = "auto"
	}
	target := "https://music.youtube.com/watch?v=" + cleanID
	switch f {
	case "mp3", "m4a":
		return y.resolveDLStream(ctx, target, f)
	case "auto":
		return y.race(ctx, target, "mp3")
	case "fast":
		return y.race(ctx, target, "")
	}
	return nil, BadReq("format: auto, fast, mp3, atau m4a")
}

// race menjalankan mp3 dan m4a bersamaan. prefer="" berarti hasil pertama langsung dipakai.
func (y *YTMusicEngine) race(ctx context.Context, target, prefer string) (*AudioStreamItem, error) {
	c, cancel := context.WithCancel(ctx)
	defer cancel()
	type res struct {
		s *AudioStreamItem
		e error
	}
	total := 2
	if prefer == "" {
		total = 3
	}
	ch := make(chan res, 3)
	for _, f := range []string{"mp3", "m4a"} {
		go func(f string) {
			s, e := y.resolveDLStream(c, target, f)
			ch <- res{s, e}
		}(f)
	}
	if prefer == "" {
		go func() {
			s, e := y.directStream(c, target[strings.LastIndex(target, "=")+1:])
			ch <- res{s, e}
		}()
	}
	var best *AudioStreamItem
	var last, dErr error
	var grace <-chan time.Time
	for n := 0; n < total; {
		select {
		case r := <-ch:
			n++
			if r.e != nil {
				if strings.HasPrefix(r.e.Error(), "direct_") {
					dErr = r.e
				} else {
					last = r.e
				}
				continue
			}
			if prefer == "" || r.s.FileType == prefer {
				return r.s, nil
			}
			best = r.s
			grace = time.After(300 * time.Millisecond)
		case <-grace:
			return best, nil
		}
	}
	if best != nil {
		return best, nil
	}
	if last != nil {
		return nil, last
	}
	return nil, dErr
}

func (y *YTMusicEngine) Download(ctx context.Context, videoID string, requestedFormat string) (*DownloadResult, error) {
	cleanID := y.extractVideoID(videoID)
	if cleanID == "" {
		return nil, BadReq("invalid_video_id: " + videoID)
	}
	targetURL := "https://music.youtube.com/watch?v=" + cleanID
	formats := []string{"mp3", "m4a"}
	if requestedFormat != "" {
		formats = []string{requestedFormat}
	}
	var streams []AudioStreamItem
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, f := range formats {
		wg.Add(1)
		go func(fmtCode string) {
			defer wg.Done()
			s, err := y.resolveDLStream(ctx, targetURL, fmtCode)
			if err == nil && s != nil {
				mu.Lock()
				streams = append(streams, *s)
				mu.Unlock()
			}
		}(f)
	}
	wg.Wait()
	if len(streams) == 0 {
		return nil, fmt.Errorf("audio_conversion_failed_for_all_formats")
	}
	return &DownloadResult{
		VideoID: cleanID, Title: "YouTube Music Audio Track", CanonicalURL: targetURL,
		Thumbnails: []ThumbnailItem{{URL: fmt.Sprintf("https://i.ytimg.com/vi/%s/hqdefault.jpg", cleanID)}},
		Streams:    streams,
	}, nil
}

func (y *YTMusicEngine) Album(ctx context.Context, input string) (*AlbumResult, error) {
	browseID := y.extractAlbumID(input)
	if browseID == "" {
		return nil, BadReq("invalid_album_id_or_url: " + input)
	}
	res, err := y.postInnerTube(ctx, "browse", map[string]any{"browseId": browseID})
	if err != nil {
		return nil, err
	}
	header := res.Get("contents.twoColumnBrowseResultsRenderer.tabs.0.tabRenderer.content.sectionListRenderer.contents.0.musicResponsiveHeaderRenderer")
	title := header.Get("title.runs.0.text").String()
	if title == "" {
		title = res.Get("microformat.microformatDataRenderer.title").String()
	}
	artist := header.Get("straplineTextOne.runs.0.text").String()
	year := ""
	for _, sr := range header.Get("subtitle.runs").Array() {
		t := strings.TrimSpace(sr.Get("text").String())
		if reYear.MatchString(t) {
			year = t
		}
	}
	thumbs := y.parseThumbnails(header.Get("thumbnail.musicThumbnailRenderer.thumbnail.thumbnails"))
	trackShelf := res.Get("contents.twoColumnBrowseResultsRenderer.secondaryContents.sectionListRenderer.contents.0.musicShelfRenderer.contents").Array()
	if len(trackShelf) == 0 {
		trackShelf = res.Get("contents.twoColumnBrowseResultsRenderer.secondaryContents.sectionListRenderer.contents.0.musicPlaylistShelfRenderer.contents").Array()
	}
	var tracks []TrackItem
	for _, tr := range trackShelf {
		rend := tr.Get("musicResponsiveListItemRenderer")
		if !rend.Exists() {
			continue
		}
		tTitle := rend.Get("flexColumns.0.musicResponsiveListItemFlexColumnRenderer.text.runs.0.text").String()
		tVid := rend.Get("playlistItemData.videoId").String()
		if tVid == "" {
			tVid = rend.Get("flexColumns.0.musicResponsiveListItemFlexColumnRenderer.text.runs.0.navigationEndpoint.watchEndpoint.videoId").String()
		}
		tArtist := rend.Get("flexColumns.1.musicResponsiveListItemFlexColumnRenderer.text.runs.0.text").String()
		if tArtist == "" {
			tArtist = artist
		}
		tDur := rend.Get("fixedColumns.0.musicResponsiveListItemFixedColumnRenderer.text.runs.0.text").String()
		tThumbs := y.parseThumbnails(rend.Get("thumbnail.musicThumbnailRenderer.thumbnail.thumbnails"))
		if len(tThumbs) == 0 {
			tThumbs = thumbs
		}
		tracks = append(tracks, TrackItem{
			VideoID: tVid, Title: tTitle, Artist: tArtist, Album: title, Duration: tDur,
			Thumbnails: tThumbs, URL: "https://music.youtube.com/watch?v=" + tVid,
		})
	}
	return &AlbumResult{BrowseID: browseID, Title: title, Artist: artist, Year: year, TrackCount: len(tracks), Thumbnails: thumbs, Tracks: tracks}, nil
}

func (y *YTMusicEngine) Playlist(ctx context.Context, input string) (*PlaylistResult, error) {
	playlistID := y.extractPlaylistID(input)
	if playlistID == "" {
		return nil, BadReq("invalid_playlist_id_or_url: " + input)
	}
	res, err := y.postInnerTube(ctx, "browse", map[string]any{"browseId": "VL" + playlistID})
	if err != nil {
		return nil, err
	}
	header := res.Get("contents.twoColumnBrowseResultsRenderer.tabs.0.tabRenderer.content.sectionListRenderer.contents.0.musicResponsiveHeaderRenderer")
	title := header.Get("title.runs.0.text").String()
	if title == "" {
		title = res.Get("microformat.microformatDataRenderer.title").String()
	}
	author := header.Get("straplineTextOne.runs.0.text").String()
	thumbs := y.parseThumbnails(header.Get("thumbnail.musicThumbnailRenderer.thumbnail.thumbnails"))
	shelf := res.Get("contents.twoColumnBrowseResultsRenderer.secondaryContents.sectionListRenderer.contents.0.musicPlaylistShelfRenderer.contents").Array()
	if len(shelf) == 0 {
		shelf = res.Get("contents.twoColumnBrowseResultsRenderer.secondaryContents.sectionListRenderer.contents.0.musicShelfRenderer.contents").Array()
	}
	var tracks []TrackItem
	for _, tr := range shelf {
		rend := tr.Get("musicResponsiveListItemRenderer")
		if !rend.Exists() {
			continue
		}
		tTitle := rend.Get("flexColumns.0.musicResponsiveListItemFlexColumnRenderer.text.runs.0.text").String()
		tVid := rend.Get("playlistItemData.videoId").String()
		if tVid == "" {
			tVid = rend.Get("flexColumns.0.musicResponsiveListItemFlexColumnRenderer.text.runs.0.navigationEndpoint.watchEndpoint.videoId").String()
		}
		tArtist := rend.Get("flexColumns.1.musicResponsiveListItemFlexColumnRenderer.text.runs.0.text").String()
		tAlbum := rend.Get("flexColumns.2.musicResponsiveListItemFlexColumnRenderer.text.runs.0.text").String()
		tDur := rend.Get("fixedColumns.0.musicResponsiveListItemFixedColumnRenderer.text.runs.0.text").String()
		tThumbs := y.parseThumbnails(rend.Get("thumbnail.musicThumbnailRenderer.thumbnail.thumbnails"))
		tracks = append(tracks, TrackItem{
			VideoID: tVid, Title: tTitle, Artist: tArtist, Album: tAlbum, Duration: tDur,
			Thumbnails: tThumbs, URL: "https://music.youtube.com/watch?v=" + tVid,
		})
	}
	return &PlaylistResult{PlaylistID: playlistID, Title: title, Author: author, TrackCount: len(tracks), Thumbnails: thumbs, Tracks: tracks}, nil
}

func (y *YTMusicEngine) extractVideoID(raw string) string {
	clean := strings.TrimSpace(raw)
	if reID11.MatchString(clean) {
		return clean
	}
	if m := reV.FindStringSubmatch(clean); len(m) > 1 {
		return m[1]
	}
	if m := reShort.FindStringSubmatch(clean); len(m) > 1 {
		return m[1]
	}
	return clean
}

func (y *YTMusicEngine) extractPlaylistID(raw string) string {
	clean := strings.TrimSpace(raw)
	if strings.HasPrefix(clean, "VL") {
		return strings.TrimPrefix(clean, "VL")
	}
	if m := reList.FindStringSubmatch(clean); len(m) > 1 {
		return m[1]
	}
	return clean
}

func (y *YTMusicEngine) extractAlbumID(raw string) string {
	clean := strings.TrimSpace(raw)
	if strings.HasPrefix(clean, "MPREb") {
		return clean
	}
	if m := reAlbum.FindStringSubmatch(clean); len(m) > 1 {
		return m[1]
	}
	if m := reOLAK.FindStringSubmatch(clean); len(m) > 1 {
		return "VL" + m[1]
	}
	if strings.HasPrefix(clean, "OLAK5uy_") {
		return "VL" + clean
	}
	return clean
}

// Klien YouTube yang dicoba bersamaan untuk URL audio langsung (tanpa converter).
type ytClient struct {
	name, version, id, ua string
	extra                 map[string]any
	embed                 bool
}

var directClients = []ytClient{
	{name: "ANDROID_VR", version: "1.60.19", id: "28", ua: "com.google.android.apps.youtube.vr.oculus/1.60.19 (Linux; U; Android 12L; eureka-user Build/SQ3A.220605.009.A1) gzip",
		extra: map[string]any{"deviceMake": "Oculus", "deviceModel": "Quest 3", "androidSdkVersion": 32, "osName": "Android", "osVersion": "12L"}},
	{name: "ANDROID_VR", version: "1.65.10", id: "28", ua: "com.google.android.apps.youtube.vr.oculus/1.65.10 (Linux; U; Android 12L; eureka-user Build/SQ3A.220605.009.A1) gzip",
		extra: map[string]any{"deviceMake": "Oculus", "deviceModel": "Quest 3", "androidSdkVersion": 32, "osName": "Android", "osVersion": "12L"}},
	{name: "IOS", version: "20.10.4", id: "5", ua: "com.google.ios.youtube/20.10.4 (iPhone16,2; U; CPU iOS 18_3_2 like Mac OS X;)",
		extra: map[string]any{"deviceMake": "Apple", "deviceModel": "iPhone16,2", "osName": "iPhone", "osVersion": "18.3.2.22D82"}},
	{name: "TVHTML5_SIMPLY_EMBEDDED_PLAYER", version: "2.0", id: "85", ua: "Mozilla/5.0 (PlayStation; PlayStation 4/12.00) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/15.4 Safari/605.1.15", embed: true},
}

func (y *YTMusicEngine) directWith(ctx context.Context, videoID string, c ytClient) (*AudioStreamItem, error) {
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
		return nil, err
	}
	dctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(dctx, http.MethodPost, "https://www.youtube.com/youtubei/v1/player?prettyPrint=false", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", c.ua)
	req.Header.Set("X-YouTube-Client-Name", c.id)
	req.Header.Set("X-YouTube-Client-Version", c.version)
	resp, err := y.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	res := gjson.ParseBytes(body)
	if st := res.Get("playabilityStatus.status").String(); st != "OK" {
		return nil, fmt.Errorf("%s:%s", c.name, st)
	}
	pick := func(prefix string) string {
		var best string
		var bestBr int64
		for _, f := range res.Get("streamingData.adaptiveFormats").Array() {
			if !strings.HasPrefix(f.Get("mimeType").String(), prefix) {
				continue
			}
			u := f.Get("url").String()
			if u == "" {
				continue
			}
			if br := f.Get("bitrate").Int(); br > bestBr {
				bestBr = br
				best = u
			}
		}
		return best
	}
	ft := "m4a"
	best := pick("audio/mp4")
	if best == "" { // cadangan: webm/opus (Chrome dan Firefox bisa memutar)
		best = pick("audio/webm")
		ft = "webm"
	}
	if best == "" {
		return nil, fmt.Errorf("%s:no_audio(url kosong, kemungkinan butuh PO token)", c.name)
	}
	return &AudioStreamItem{Format: ft, Quality: "direct", FileType: ft, DownloadURL: best, Status: "Available", Direct: true, UA: c.ua}, nil
}

// directStream: URL audio langsung dari YouTube, semua klien dicoba bersamaan, yang pertama berhasil dipakai.
// URL terikat IP server, jadi diputar lewat proxy (Direct=true).
func (y *YTMusicEngine) directStream(ctx context.Context, videoID string) (*AudioStreamItem, error) {
	c, cancel := context.WithCancel(ctx)
	defer cancel()
	type dres struct {
		s *AudioStreamItem
		e error
	}
	ch := make(chan dres, len(directClients))
	for _, cl := range directClients {
		go func(cl ytClient) {
			s, e := y.directWith(c, videoID, cl)
			ch <- dres{s, e}
		}(cl)
	}
	var errs []string
	for range directClients {
		x := <-ch
		if x.e == nil {
			return x.s, nil
		}
		errs = append(errs, x.e.Error())
	}
	return nil, fmt.Errorf("direct_unavailable: %s", strings.Join(errs, " | "))
}
