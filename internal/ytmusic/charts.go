package ytmusic

// v3.7: chart dunia dengan sumber acak (Spotify / Apple Music / YouTube Music),
// cover album per lagu (bukan cover playlist), "Lagi viral" dari Spotify Viral 50,
// dan gambar genre dari Apple Music.

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tidwall/gjson"
)

// chartSeed: satu lagu mentah dari chart. Track terisi bila sumbernya sudah YouTube Music
// (video ID dan cover sudah ada, tidak perlu dicari lagi).
type chartSeed struct {
	Title, Artist, Artwork, Source string
	Track                          *TrackItem
}

var reArtBB = regexp.MustCompile(`\d+x\d+bb`)

func appleArt(u string) string {
	if u == "" {
		return ""
	}
	return reArtBB.ReplaceAllString(u, "600x600bb")
}

func getBytes(ctx context.Context, u string, to time.Duration) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Jemci/3.7")
	cl := &http.Client{Timeout: to}
	resp, err := cl.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}

// ---------- Apple Music ----------

// fetchApple: chart lagu Apple Music satu negara. Coba RSS marketing tools, cadangan RSS iTunes lama.
func fetchApple(ctx context.Context, cc string, n int) ([]chartSeed, error) {
	var out []chartSeed
	if b, err := getBytes(ctx, fmt.Sprintf("https://rss.applemarketingtools.com/api/v2/%s/music/most-played/%d/songs.json", cc, n), 10*time.Second); err == nil {
		for _, x := range gjson.GetBytes(b, "feed.results").Array() {
			name := strings.TrimSpace(x.Get("name").String())
			if name == "" {
				continue
			}
			out = append(out, chartSeed{Title: name, Artist: x.Get("artistName").String(), Artwork: appleArt(x.Get("artworkUrl100").String()), Source: "apple"})
		}
	}
	if len(out) == 0 {
		b, err := getBytes(ctx, fmt.Sprintf("https://itunes.apple.com/%s/rss/topsongs/limit=%d/json", cc, n), 10*time.Second)
		if err != nil {
			return nil, err
		}
		for _, x := range gjson.GetBytes(b, "feed.entry").Array() {
			name := strings.TrimSpace(x.Get("im:name.label").String())
			if name == "" {
				continue
			}
			art := ""
			imgs := x.Get("im:image").Array()
			if len(imgs) > 0 {
				art = appleArt(imgs[len(imgs)-1].Get("label").String())
			}
			out = append(out, chartSeed{Title: name, Artist: x.Get("im:artist.label").String(), Artwork: art, Source: "apple"})
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("chart Apple %s kosong", cc)
	}
	return out, nil
}

// appleGlobalSeeds: Apple tidak punya chart "global" di RSS publik, jadi chart beberapa negara besar
// digabung dengan sistem poin (peringkat tinggi + muncul di banyak negara = naik).
func appleGlobalSeeds(ctx context.Context, n int) ([]chartSeed, error) {
	ccs := []string{"us", "gb", "jp", "br", "id", "de", "mx", "kr"}
	lists := make([][]chartSeed, len(ccs))
	var wg sync.WaitGroup
	for i, cc := range ccs {
		wg.Add(1)
		go func(i int, cc string) {
			defer wg.Done()
			l, err := fetchApple(ctx, cc, 25)
			if err == nil {
				lists[i] = l
			}
		}(i, cc)
	}
	wg.Wait()
	type agg struct {
		seed        chartSeed
		score, hits int
	}
	m := map[string]*agg{}
	for _, l := range lists {
		for i, s := range l {
			first := ""
			if parts := strings.FieldsFunc(s.Artist, func(r rune) bool { return r == ',' || r == '&' }); len(parts) > 0 {
				first = strings.ToLower(strings.TrimSpace(parts[0]))
			}
			key := strings.ToLower(strings.TrimSpace(s.Title)) + "|" + first
			a := m[key]
			if a == nil {
				a = &agg{seed: s}
				m[key] = a
			}
			a.score += 26 - i
			a.hits++
		}
	}
	if len(m) == 0 {
		return nil, fmt.Errorf("semua chart Apple gagal dibaca")
	}
	all := make([]*agg, 0, len(m))
	for _, a := range m {
		all = append(all, a)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].score != all[j].score {
			return all[i].score > all[j].score
		}
		return all[i].hits > all[j].hits
	})
	out := make([]chartSeed, 0, n)
	for _, a := range all {
		out = append(out, a.seed)
		if len(out) >= n {
			break
		}
	}
	return out, nil
}

// ---------- Spotify ----------

// spotifySeeds: isi playlist Spotify. Cover playlist TIDAK pernah dipakai sebagai cover lagu;
// artwork yang sama berulang (tanda salah ambil) juga dibuang, nanti dicari per lagu.
func spotifySeeds(ctx context.Context, id string, n int) ([]chartSeed, error) {
	sp, err := Spotify(ctx, "playlist", id)
	if err != nil {
		return nil, err
	}
	cnt := map[string]int{}
	for _, t := range sp.Tracks {
		if t.Artwork != "" {
			cnt[t.Artwork]++
		}
	}
	out := []chartSeed{}
	for _, t := range sp.Tracks {
		if strings.TrimSpace(t.Title) == "" {
			continue
		}
		art := t.Artwork
		if art != "" && (art == sp.CoverURL || cnt[art] > 1) {
			art = ""
		}
		out = append(out, chartSeed{Title: t.Title, Artist: t.Artist, Artwork: art, Source: "spotify"})
		if len(out) >= n {
			break
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("playlist Spotify kosong")
	}
	return out, nil
}

// ---------- YouTube Music ----------

// ytChartSeeds: playlist resmi "Top 100 Songs Global" milik YouTube Music. Hanya dipakai bila
// judul playlist yang ditemukan benar-benar cocok, supaya tidak salah ambil playlist lain.
func (y *YTMusicEngine) ytChartSeeds(ctx context.Context, n int) ([]chartSeed, error) {
	res, err := y.Search(ctx, "Top 100 Songs Global YouTube Music Charts")
	if err != nil {
		return nil, err
	}
	for _, p := range res.Playlists {
		t := strings.ToLower(p.Title)
		if p.PlaylistID == "" || !strings.Contains(t, "top 100 songs") || !strings.Contains(t, "global") {
			continue
		}
		pl, err := y.Playlist(ctx, p.PlaylistID)
		if err != nil {
			return nil, err
		}
		out := []chartSeed{}
		for i := range pl.Tracks {
			tr := pl.Tracks[i]
			if tr.VideoID == "" || strings.TrimSpace(tr.Title) == "" {
				continue
			}
			out = append(out, chartSeed{Title: tr.Title, Artist: tr.Artist, Source: "youtube", Track: &tr})
			if len(out) >= n {
				break
			}
		}
		if len(out) > 0 {
			return out, nil
		}
	}
	return nil, fmt.Errorf("playlist Top 100 Songs Global tidak ditemukan")
}

// ---------- cover Apple per lagu ----------

var (
	itSem   = make(chan struct{}, 2)
	itCache sync.Map
)

func normTitle(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// appleArtwork: cover album 600px dari iTunes Search untuk satu lagu. Dibatasi 2 permintaan bersamaan
// dan hasilnya disimpan di memori, karena iTunes membatasi jumlah permintaan per menit.
func appleArtwork(ctx context.Context, title, artist string) string {
	key := normTitle(title + "|" + artist)
	if v, ok := itCache.Load(key); ok {
		return v.(string)
	}
	select {
	case itSem <- struct{}{}:
		defer func() { <-itSem }()
	case <-ctx.Done():
		return ""
	}
	b, err := getBytes(ctx, "https://itunes.apple.com/search?entity=song&limit=6&term="+url.QueryEscape(strings.TrimSpace(artist+" "+title)), 7*time.Second)
	if err != nil {
		return ""
	}
	want := normTitle(title)
	res := ""
	for _, x := range gjson.GetBytes(b, "results").Array() {
		art := x.Get("artworkUrl100").String()
		if art == "" {
			continue
		}
		got := normTitle(x.Get("trackName").String())
		if got == want || (len(want) > 4 && (strings.Contains(got, want) || strings.Contains(want, got))) {
			res = appleArt(art)
			break
		}
	}
	itCache.Store(key, res)
	return res
}

// coverAlbum true bila thumbnail berasal dari cover album (kotak), bukan potongan video YouTube.
func coverAlbum(th []ThumbnailItem) bool {
	for _, t := range th {
		if strings.Contains(t.URL, "googleusercontent.com") || strings.Contains(t.URL, "ggpht.com") {
			return true
		}
	}
	return false
}

func (y *YTMusicEngine) resolveSeed(ctx context.Context, seed chartSeed) (TrackItem, bool) {
	if seed.Track != nil && seed.Track.VideoID != "" {
		out := *seed.Track
		if !coverAlbum(out.Thumbnails) {
			if u := appleArtwork(ctx, out.Title, out.Artist); u != "" {
				out.Thumbnails = []ThumbnailItem{{URL: u, Width: 600, Height: 600}}
			}
		}
		return out, true
	}
	q := strings.TrimSpace(seed.Artist + " " + seed.Title)
	res, err := y.Search(ctx, q)
	if err != nil || res == nil {
		return TrackItem{}, false
	}
	needle := strings.ToLower(strings.TrimSpace(seed.Title))
	var best *TrackItem
	for i := range res.Songs {
		t := &res.Songs[i]
		if t.VideoID == "" {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(t.Title))
		if name == needle {
			best = t
			break
		}
		if best == nil && (strings.Contains(name, needle) || strings.Contains(needle, name)) {
			best = t
		}
	}
	if best == nil {
		for i := range res.Songs {
			if res.Songs[i].VideoID != "" {
				best = &res.Songs[i]
				break
			}
		}
	}
	if best == nil {
		return TrackItem{}, false
	}
	out := *best
	if seed.Artist != "" {
		out.Artist = seed.Artist
	}
	switch {
	case seed.Artwork != "":
		out.Thumbnails = []ThumbnailItem{{URL: seed.Artwork, Width: 600, Height: 600}}
	case !coverAlbum(out.Thumbnails):
		if u := appleArtwork(ctx, seed.Title, seed.Artist); u != "" {
			out.Thumbnails = []ThumbnailItem{{URL: u, Width: 600, Height: 600}}
		}
	}
	return out, true
}

// resolveSeeds memetakan lagu chart ke video YouTube Music (supaya tombol putar tetap jalan),
// lengkap dengan cover per lagu. Urutan chart dipertahankan.
func (y *YTMusicEngine) resolveSeeds(ctx context.Context, seeds []chartSeed, limit int) *DiscoverResult {
	if len(seeds) > limit {
		seeds = seeds[:limit]
	}
	type item struct {
		i  int
		t  TrackItem
		ok bool
	}
	ch := make(chan item, len(seeds))
	sem := make(chan struct{}, 6)
	var wg sync.WaitGroup
	for i, seed := range seeds {
		wg.Add(1)
		go func(i int, seed chartSeed) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			c, cancel := context.WithTimeout(ctx, 12*time.Second)
			defer cancel()
			t, ok := y.resolveSeed(c, seed)
			ch <- item{i, t, ok}
		}(i, seed)
	}
	wg.Wait()
	close(ch)
	ordered := make([]TrackItem, len(seeds))
	valid := make([]bool, len(seeds))
	for x := range ch {
		if x.ok {
			ordered[x.i] = x.t
			valid[x.i] = true
		}
	}
	out := &DiscoverResult{Songs: []TrackItem{}, Artists: []ArtistItem{}}
	seenSong, seenArtist := map[string]bool{}, map[string]bool{}
	for i, t := range ordered {
		if !valid[i] || t.VideoID == "" || seenSong[t.VideoID] {
			continue
		}
		seenSong[t.VideoID] = true
		out.Songs = append(out.Songs, t)
		for _, a := range strings.Split(t.Artist, ",") {
			a = strings.TrimSpace(a)
			k := strings.ToLower(a)
			if a != "" && !seenArtist[k] {
				seenArtist[k] = true
				out.Artists = append(out.Artists, ArtistItem{Name: a})
			}
		}
	}
	// Pengaman: bila satu gambar dipakai 3 lagu atau lebih, itu pasti cover yang salah (mis. cover playlist).
	// Gambar itu dibuang; browser lalu memakai gambar video YouTube milik lagu masing-masing.
	cnt := map[string]int{}
	for _, t := range out.Songs {
		if len(t.Thumbnails) > 0 {
			cnt[t.Thumbnails[0].URL]++
		}
	}
	for i := range out.Songs {
		t := &out.Songs[i]
		if len(t.Thumbnails) > 0 && cnt[t.Thumbnails[0].URL] >= 3 {
			t.Thumbnails = nil
		}
	}
	return out
}

// Global20: lagu trending dunia. Sumber dipilih ACAK dari Spotify Global Top 50, Apple Music
// (gabungan chart banyak negara), dan YouTube Music Top 100 Global; bila satu gagal, pindah ke yang lain.
func (y *YTMusicEngine) Global20(ctx context.Context) (*DiscoverResult, error) {
	order := []string{"spotify", "apple", "youtube"}
	rand.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
	var errs []string
	for _, src := range order {
		var seeds []chartSeed
		var err error
		switch src {
		case "spotify":
			seeds, err = spotifySeeds(ctx, "37i9dQZEVXbMDoHDwVN2tF", 20)
		case "apple":
			seeds, err = appleGlobalSeeds(ctx, 20)
		default:
			seeds, err = y.ytChartSeeds(ctx, 20)
		}
		if err != nil || len(seeds) < 8 {
			errs = append(errs, fmt.Sprintf("%s: %v", src, err))
			continue
		}
		sc, cancel := context.WithTimeout(ctx, 22*time.Second)
		res := y.resolveSeeds(sc, seeds, 20)
		cancel()
		if len(res.Songs) >= 8 {
			res.Source = src
			return res, nil
		}
		errs = append(errs, fmt.Sprintf("%s: hanya %d lagu cocok", src, len(res.Songs)))
	}
	return nil, fmt.Errorf("semua chart gagal: %s", strings.Join(errs, "; "))
}

// Viral20: "Lagi viral" di pencarian, semua dari Spotify. Urutan: Viral 50 Global, lalu playlist
// Viral Hits, lalu Global Top 50 (lagu urutan 21 ke bawah supaya tidak sama dengan chart beranda).
func (y *YTMusicEngine) Viral20(ctx context.Context) (*DiscoverResult, error) {
	lists := []struct {
		id   string
		skip int
	}{
		{"37i9dQZEVXbLiRSasKsNU9", 0},
		{"37i9dQZF1DX2L0iB23Enbq", 0},
		{"37i9dQZEVXbMDoHDwVN2tF", 20},
	}
	var errs []string
	for _, l := range lists {
		seeds, err := spotifySeeds(ctx, l.id, l.skip+20)
		if err != nil {
			errs = append(errs, l.id+": "+err.Error())
			continue
		}
		if l.skip > 0 {
			if len(seeds) <= l.skip {
				continue
			}
			seeds = seeds[l.skip:]
		}
		sc, cancel := context.WithTimeout(ctx, 22*time.Second)
		res := y.resolveSeeds(sc, seeds, 20)
		cancel()
		if len(res.Songs) >= 4 {
			res.Source = "spotify"
			return res, nil
		}
		errs = append(errs, fmt.Sprintf("%s: hanya %d lagu cocok", l.id, len(res.Songs)))
	}
	return nil, fmt.Errorf("lagu viral Spotify tidak bisa dimuat: %s", strings.Join(errs, "; "))
}

// ---------- gambar genre dari Apple Music ----------

var genreIDs = map[string]string{
	"Pop": "14", "Rock": "21", "Hip-Hop": "18", "R&B": "15", "EDM": "17", "Jazz": "2",
	"Lo-fi": "", "Indie": "20", "Country": "6", "K-Pop": "51", "Latin": "12",
	"Akustik": "10", "Klasik": "5", "Metal": "1153", "Reggae": "24",
}

var genreTerms = map[string]string{
	"Lo-fi": "lofi beats", "Pop": "pop hits", "Rock": "rock hits", "Hip-Hop": "hip hop hits",
	"R&B": "r&b hits", "EDM": "edm hits", "Jazz": "jazz classics", "Indie": "indie pop",
	"Country": "country hits", "K-Pop": "kpop", "Latin": "latin hits", "Akustik": "acoustic",
	"Klasik": "classical piano", "Metal": "metal hits", "Reggae": "reggae hits",
}

// GenreArt: gambar kartu genre dari chart genre Apple Music (cadangan: pencarian iTunes).
func GenreArt(ctx context.Context, g string) (map[string]string, error) {
	id, ok := genreIDs[g]
	if !ok {
		return nil, BadReq("genre tidak dikenal")
	}
	if id != "" {
		if b, err := getBytes(ctx, "https://itunes.apple.com/us/rss/topsongs/limit=3/genre="+id+"/json", 9*time.Second); err == nil {
			for _, x := range gjson.GetBytes(b, "feed.entry").Array() {
				imgs := x.Get("im:image").Array()
				if len(imgs) > 0 {
					if u := appleArt(imgs[len(imgs)-1].Get("label").String()); u != "" {
						return map[string]string{"cover_url": u, "source": "apple"}, nil
					}
				}
			}
		}
	}
	b, err := getBytes(ctx, "https://itunes.apple.com/search?entity=song&limit=5&term="+url.QueryEscape(genreTerms[g]), 9*time.Second)
	if err != nil {
		return nil, err
	}
	for _, x := range gjson.GetBytes(b, "results").Array() {
		if u := appleArt(x.Get("artworkUrl100").String()); u != "" {
			return map[string]string{"cover_url": u, "source": "apple"}, nil
		}
	}
	return nil, fmt.Errorf("gambar genre Apple tidak tersedia")
}
