package ytmusic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// SpotifyTrack: satu lagu dari playlist/album/track Spotify (hanya judul dan artis).
type SpotifyTrack struct {
	Title   string `json:"title"`
	Artist  string `json:"artist"`
	Artwork string `json:"artwork_url,omitempty"`
}

// SpotifyResult: isi satu link Spotify.
type SpotifyResult struct {
	Name     string         `json:"name"`
	CoverURL string         `json:"cover_url,omitempty"`
	Tracks   []SpotifyTrack `json:"tracks"`
}

var spClient = &http.Client{Timeout: 20 * time.Second}
var spData = regexp.MustCompile(`(?s)<script id="__NEXT_DATA__"[^>]*>(.*?)</script>`)
var spID = regexp.MustCompile(`^[A-Za-z0-9]{10,30}$`)

func str(m map[string]any, k string) string { s, _ := m[k].(string); return s }

func spotifyCover(v any) string {
	switch x := v.(type) {
	case map[string]any:
		if vi, ok := x["visualIdentity"].(map[string]any); ok {
			if imgs, ok := vi["image"].([]any); ok {
				for i := len(imgs) - 1; i >= 0; i-- {
					if im, ok := imgs[i].(map[string]any); ok && str(im, "url") != "" {
						return str(im, "url")
					}
				}
			}
		}
		for _, child := range x {
			if u := spotifyCover(child); u != "" {
				return u
			}
		}
	case []any:
		for _, child := range x {
			if u := spotifyCover(child); u != "" {
				return u
			}
		}
	}
	return ""
}

// trackArtwork mengambil artwork album per lagu dari struktur embed Spotify.
// Playlist cover tidak dipakai sebagai cover setiap track.
func imageURL(v any) string {
	switch x := v.(type) {
	case map[string]any:
		if u := str(x, "url"); strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://") {
			return u
		}
		for _, child := range x {
			if u := imageURL(child); u != "" {
				return u
			}
		}
	case []any:
		for _, child := range x {
			if u := imageURL(child); u != "" {
				return u
			}
		}
	}
	return ""
}

func trackArtwork(v any) string {
	switch x := v.(type) {
	case map[string]any:
		for _, key := range []string{"coverArt", "albumOfTrack", "album", "images", "visualIdentity", "image"} {
			if child, ok := x[key]; ok {
				if u := imageURL(child); u != "" {
					return u
				}
			}
		}
	case []any:
		for _, child := range x {
			if u := trackArtwork(child); u != "" {
				return u
			}
		}
	}
	return ""
}

// walk mencari objek yang punya trackList (playlist/album) atau artists (track) di JSON halaman embed.
func walk(v any, res *SpotifyResult) {
	switch x := v.(type) {
	case map[string]any:
		if tl, ok := x["trackList"].([]any); ok && len(res.Tracks) == 0 {
			res.Name = str(x, "name")
			if res.Name == "" {
				res.Name = str(x, "title")
			}
			for _, it := range tl {
				if m, ok := it.(map[string]any); ok && str(m, "title") != "" {
					res.Tracks = append(res.Tracks, SpotifyTrack{Title: str(m, "title"), Artist: strings.ReplaceAll(str(m, "subtitle"), "\u00a0", " "), Artwork: trackArtwork(m)})
				}
			}
			return
		}
		if as, ok := x["artists"].([]any); ok && str(x, "type") == "track" && len(res.Tracks) == 0 {
			var n []string
			for _, a := range as {
				if am, ok := a.(map[string]any); ok && str(am, "name") != "" {
					n = append(n, str(am, "name"))
				}
			}
			res.Tracks = append(res.Tracks, SpotifyTrack{Title: str(x, "name"), Artist: strings.Join(n, ", "), Artwork: trackArtwork(x)})
			res.Name = str(x, "name")
			return
		}
		for _, c := range x {
			walk(c, res)
		}
	case []any:
		for _, c := range x {
			walk(c, res)
		}
	}
}

// Spotify membaca halaman embed publik (tanpa login/API key). Playlist dibatasi ~100 lagu pertama oleh Spotify.
func Spotify(ctx context.Context, typ, id string) (*SpotifyResult, error) {
	if typ != "playlist" && typ != "album" && typ != "track" {
		return nil, BadReq("tipe harus playlist, album, atau track")
	}
	aliases := map[string]string{
		"tophits": "37i9dQZF1DXcBWIGoYBM5M", "chillhits": "37i9dQZF1DX4WYpdgoIcn6",
		"rapcaviar": "37i9dQZF1DX0XUsuxWHRQd", "rockclassics": "37i9dQZF1DWXRqgorJj26U",
		"popallstars": "37i9dQZF1DX0kbEb7clvgq", "kpop": "37i9dQZF1DX9tPFteotfr7",
		"animehits": "37i9dQZF1DX6XceWZJ1znY", "allout2020s": "37i9dQZF1DX2M1R2ehsrGH",
		"allout2010s": "37i9dQZF1DX5Ejj0EkURtP", "allout2000s": "37i9dQZF1DX4o1oenSJRJd",
		"allout90s": "37i9dQZF1DXbTxeAdrVG2l", "allout80s": "37i9dQZF1DX4UtSsGT1Sbe",
	}
	if real, ok := aliases[strings.ToLower(strings.TrimSpace(id))]; ok {
		id = real
	}
	if !spID.MatchString(id) {
		return nil, BadReq("id atau alias Spotify tidak valid")
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://open.spotify.com/embed/"+typ+"/"+id, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 13) AppleWebKit/537.36 Chrome/124 Mobile Safari/537.36")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	resp, err := spClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	m := spData.FindSubmatch(body)
	if resp.StatusCode != 200 || m == nil {
		return nil, BadReq("Spotify tidak bisa dibaca (playlist privat atau format berubah). Coba tempel daftar judul lagu saja.")
	}
	var j any
	if json.Unmarshal(m[1], &j) != nil {
		return nil, BadReq("data Spotify rusak")
	}
	res := &SpotifyResult{CoverURL: spotifyCover(j)}
	walk(j, res)
	if len(res.Tracks) == 0 {
		return nil, BadReq("tidak ada lagu ditemukan. Pastikan playlist publik.")
	}
	return res, nil
}
