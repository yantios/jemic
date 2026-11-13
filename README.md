# Jemci

Frontend (`public/`) + backend Go (`/api/*`) dalam satu server, siap deploy di Railway. Logic memakai `ytmusic.go` milik Jelan.

## Struktur
- `public/` : seluruh UI (`index.html`, `sw.js`, `manifest.json`, ikon, foto). Hanya folder ini yang tersaji ke publik.
- `main.go` : server (route /api/*, /healthz, file statis, shutdown halus)
- `internal/ytmusic/engine.go` : logic asli (Search, PlayMeta, Stream, Download, Album, Playlist)
- `internal/ytmusic/server.go` : CORS, rate limit 240/menit/IP, cache in-memory, timeout
- `internal/ytmusic/proxy.go` : proxy audio dan gambar
- `Dockerfile`, `railway.json` : konfigurasi build/deploy Railway

## Deploy ke Railway
1. Push folder ini ke GitHub (jangan commit file `.env`).
2. Railway > New Project > Deploy from GitHub repo > pilih repo ini. Railway otomatis memakai `Dockerfile`.
3. Tab Variables, tambahkan `DL_API_KEY` (key downloader kamu). Opsional: `DL_PRIMARY`, `DL_FALLBACK`, `PROXY_ALLOW`.
4. Tab Settings > Networking > Generate Domain. Selesai.
`go.sum` dibuat otomatis saat build (`go mod tidy` ada di Dockerfile), jadi tidak perlu dibuat manual. Healthcheck: `/healthz`.

## Tes lokal (Termux / PC)
```
pkg install golang
echo 'DL_API_KEY=isi_key_kamu' > .env
sh jalankan.sh
```
Buka http://localhost:8080. Port bisa diubah lewat env `PORT`.

## Yang perlu kamu ganti
- Foto developer: `public/foto-jelan.jpg`. Logo: `public/logo.png`.
- Link APK: cari `id="apk"` di `public/index.html`.

## Catatan
- Key downloader tidak hardcode. Key lama pernah terkirim di chat dan sempat ada di `.env.example`, sebaiknya diganti.
- `/api/stream` bisa lambat (polling converter). UI menampilkan "Menyiapkan audio...".
- Mengambil audio YouTube lewat converter pihak ketiga melanggar ketentuan YouTube. Pakai untuk proyek pribadi/belajar.

## v3.8
- Video lagu: tidak lagi memakai embed YouTube (jadi tidak ada judul/logo/tombol YouTube). Server memberi MP4 langsung lewat `/api/video?id=` (Range, seek instan), diputar di elemen `<video>` bisu 16:9 lebar penuh. Audio tetap dari `<audio>` Jemci sebagai jam utama; selisih kecil dikejar dengan mengubah kecepatan putar halus, selisih besar baru di-seek, jadi tidak ada lag/jeda suara. Pil Audio | Video di bawah cover.
- Lirik: pencocokan ketat ke LRCLIB (judul + artis + durasi, ditolak bila beda jauh), cache lama diganti (`ly2:`). Lirik tersinkron hanya dipakai bila durasi cocok (<=3,5 dtk); selain itu tampil sebagai teks biasa.
- Lagi viral: Spotify Viral 50 Global, lalu Viral 50 USA, Apple Music, YouTube Music (cadangan otomatis). Tampil sebagai grid 3 kolom.
- Mini player: geser ke bawah untuk menutup lagu (musik berhenti, mini player hilang).
- Liquid Glass navbar: lensa saat ditahan memperbesar ikon dan tulisan lewat ukuran layout (bukan scale), sehingga tajam seperti di-zoom.

## v3.7
- Cover album per lagu: cover playlist tidak pernah dipakai sebagai cover lagu. Urutan sumber cover: artwork Spotify (bila unik) -> cover album YouTube Music -> Apple Music (iTunes Search). Bila satu gambar dipakai 3 lagu atau lebih, gambar itu dibuang otomatis.
- Banner dan Global Top 20: sumber dipilih ACAK (di-cache 30 menit) dari Spotify Global Top 50, Apple Music (gabungan chart 8 negara besar), dan YouTube Music Top 100 Songs Global. Bila satu sumber gagal, pindah ke sumber lain. Label banner mengikuti sumber (`source` di `/api/global`).
- "Lagi viral" di Mencari: Spotify Viral 50 Global (`/api/viral`).
- Gambar kartu genre: chart genre Apple Music (`/api/genreart?g=Pop`).
- Perbaikan audio gagal dimuat:
  - `warm` di UI selalu memicu converter (warm=2) untuk setiap kartu yang disentuh/di-hover, sehingga kuota converter cepat habis. Sekarang kartu biasa hanya jalur langsung (warm=1), converter hanya untuk lagu berikutnya di antrean.
  - `/api/audio` tidak lagi diblokir `PROXY_ALLOW` (bila variabel ini terisi, semua audio dulu gagal 502 dan UI menampilkan kode 4).
  - Klien YouTube ANDROID_VR versi baru ikut berlomba, dan webm/opus jadi cadangan bila m4a tidak ada.
  - DNS gagal sesaat tidak langsung dianggap alamat privat.
  - Endpoint baru `/api/diag?id=VIDEO_ID`: menunjukkan penyebab nyata (DL_API_KEY kosong, YouTube menolak, dst). UI memakainya saat audio gagal.

## v3.6
- Banner beranda memakai Spotify Global Top 50; setiap kartu menggunakan artwork album per lagu (bukan cover playlist yang sama).
- Chart global mengambil urutan Spotify, memetakan lagu ke YouTube Music, dan memakai chart Apple sebagai fallback bila embed Spotify tidak tersedia.
- Persiapan audio dilakukan lebih awal dengan jalur langsung + converter yang digabung (single-flight), sehingga klik lagu yang sudah dipanaskan memakai URL audio cache.
- Waktu tunggu pencarian URL audio dibatasi lebih ketat; service worker dinaikkan agar browser mengambil UI baru.

## v3.4
- Audio jauh lebih cepat: jalur langsung YouTube dan converter berlomba bersamaan (converter ikut setelah 1,2 dtk atau langsung bila jalur langsung gagal), permintaan sama berbagi satu pekerjaan (tidak ada konversi ganda), kunci 2 menit dihapus, URL disimpan sesuai masa berlaku asli, cache DNS proxy. `warm=1` jalur langsung saja, `warm=2` (lagu berikutnya) dengan converter.
- Pengaturan bawaan: hanya aktif Efek blur, Liquid Glass, Efek lensa, Animasi lirik karaoke, Blur baris jauh, Ikuti lirik otomatis, Audio Dinamis, Siapkan lagu lebih awal, Putar lagu serupa otomatis, Tampilkan opening. Sisanya mati. Pengguna lama direset sekali ke bawaan baru (tata letak, volume, EQ, ukuran lirik dipertahankan).
- Kartu genre memakai gambar (sampul lagu teratas tiap genre, disimpan 7 hari).
- Fitur baru: cari dengan suara, bagikan lagu (link `/?v=ID` langsung membuka lagu), sleep timer "Akhir lagu", baris "Untukmu" (opsional, mati bawaan).

## v3.3
- Audio instan: `<audio>` langsung memakai `/api/audio?id=` (stream mengalir dari server, tanpa menunggu JSON/converter). Server mencoba URL langsung YouTube dulu, cadangan converter; lagu berikutnya dan lagu yang disentuh/di-hover disiapkan lebih dulu (`&warm=1`).
- Tema: nama dan swatch sesuai warna asli; memilih tema mematikan "Warna ikut cover lagu" (sebelumnya menimpa tema).

## v3.2
- Lagu lebih cepat bunyi: mode cepat sekarang juga mencoba stream langsung dari YouTube (`directStream`, lewat /api/proxy) berbarengan dengan converter; yang pertama selesai dipakai.
- Antrean otomatis banyak: `/api/play` memakai radio `RDAMVM<id>` (sampai 40 lagu), antrean terisi lagi otomatis saat sisa < 8, ada tombol Acak dan Tambah, antrean tersimpan.
- Video tanpa UI YouTube (di-crop kuat, tanpa teks memuat, disiapkan lebih awal), plus Mode Sinema (sembunyikan kontrol).

## v3.0
- Tampilan laptop/PC penuh (lebar >= 900px, otomatis): sidebar kiri, konten lebar, mini player jadi bar bawah (seek, volume, lirik, video), pemutar penuh dua kolom, dialog di tengah, panah geser + seret mouse di baris lagu, pintasan keyboard. Di HP tampilan lama tetap. Bisa dipaksa lewat Pengaturan > Tata letak.
- Video lagu ala Spotify (v3.1: latar layar penuh tanpa UI YouTube, tidak bisa disentuh, lirik lebih besar): ikon video di samping ikon tema pada pemutar. Klip dibisukan dan disinkronkan ke audio, tampil di samping cover (desktop) atau menggantikan cover (HP). Ada koreksi sinkron per lagu (-0.5s/+0.5s), Ganti video, layar penuh. Backend: `/api/play` kini mengirim `mv_id` (klip resmi) dan ada endpoint baru `/api/mv?q=` (cadangan pencarian video).
- Animasi lirik diperbaiki: isi warna per kata (bukan satu gradien per baris), scroll pegas yang tidak patah, titik instrumental yang terisi, klik baris langsung lanjut ikuti, jeda scroll manual yang benar, blur ringan.
- Pengaturan baru: Tata letak, Video lagu, Lirik (blur, rata teks, ikuti otomatis), Fade-in, Volume, Layar tetap menyala, Judul tab ikut lagu, Pintasan keyboard, Backup/Pulihkan data.

## v2.0
- Liquid Glass (Pengaturan > Liquid Glass, bisa ON/OFF): navbar, mini player, saklar, dan slider bergaya kaca. Tahan lalu geser navbar untuk pil kaca berlensa yang memperbesar ikon. Efek refraksi asli (SVG displacement) hanya di Chrome/Android, browser lain memakai blur kaca. Ada slider kekuatan blur.
- Tema ikut cover: warna aksen, latar, dan kartu mengikuti sampul lagu (warna diambil lewat `/api/img`, transisi halus). Bisa dimatikan.
- Lirik: fokus blur berdasarkan jarak, scroll berirama, isi warna karaoke, animasi masuk, titik animasi untuk bagian instrumental. Scroll manual dijeda 3,5 detik.
- Import playlist Spotify (Pengaturan > Playlist, atau Pustaka > Playlist): tempel link playlist/album/lagu Spotify (publik, endpoint baru `/api/spotify`, membaca halaman embed, maksimal ~100 lagu per playlist) atau daftar judul satu per baris. Tiap lagu dicari di YouTube Music lalu disimpan sebagai playlist lokal.

## v1.9 (pindah ke Railway)
- Vercel dihapus (`api/`, `vercel.json`, batas proxy 4 MB). Server tunggal `main.go` + `Dockerfile` + `railway.json`.
- Perbaikan error deploy: `go.sum` hilang dan semua file `api/*.go` berisi fungsi `Handler` yang sama sehingga `go build ./...` gagal.
- Keamanan: file statis hanya dari `public/` (sebelumnya seluruh folder, termasuk `.env` dan kode, bisa diakses lewat URL). Rate limit membuang data lama supaya memori tidak membengkak.

## v1.3
- `/api/proxy` sekarang menerima semua host publik (host lokal/privat diblokir). Batasi dengan env `PROXY_ALLOW` bila perlu.
- `/api/img` = proxy gambar cover (cadangan kalau gambar langsung gagal).
- Audio Dinamis dan Equalizer memakai Web Audio; audio otomatis lewat proxy supaya bisa diproses.

## v1.3.1 (perbaikan error saat memutar)
- Link audio dari converter bisa kedaluwarsa tapi masih tersimpan di cache (browser 20 menit, server 15 menit). Sekarang kalau audio gagal, aplikasi otomatis mengambil link baru satu kali, dan tombol play setelah error juga memakai link baru.
- Pesan error converter (API key salah, kuota habis, dll) sekarang tampil jelas, bukan "<nil>".
- (Pemotongan 4 MB khusus Vercel sudah dihapus di v1.9; di Railway audio mengalir penuh.)
- File contoh env diganti nama jadi `.env.example` dan key aslinya dihapus.

## v1.4
- Identitas baru monochrome hitam-putih: logo gerbang baru (navbar, favicon, manifest, halaman developer), tema 1 jadi hitam-putih.
- Pembuka sinematik CSS murni (~8 detik): logo reveal, gerbang muncul dari gelap, rune menyala, pintu terbuka, cahaya putih, transisi ke beranda. Hanya jalan di sesi baru, tombol "Lewati" selalu ada, dan otomatis hilang walau animasi gagal. Mendukung reduced-motion.
- Audio lebih cepat siap: polling converter 250 ms (sebelumnya 400 ms), batas waktu per request, tunggu MP3 maksimal 0,8 dtk (sebelumnya 1,5 dtk).

## v1.5 (Jemci)
- Nama baru Jemci, logo baru (`logo.png`, ikon PWA PNG), pintu gerbang dihapus, opening baru: logo muncul dengan animasi halus (bisa dimatikan di Pengaturan > Tampilkan opening).
- Audio lebih cepat siap: bawaan sekarang mode cepat (pakai hasil converter yang selesai duluan; HD MP3 320 bisa dinyalakan di Pengaturan). Server mencoba domain cadangan bersamaan bila domain utama lambat, polling 180 ms, permintaan identik digabung (tidak konversi dua kali), lagu berikutnya di-prefetch lebih awal.
- Tampilan lebih halus: transisi halaman, animasi kartu bertahap, tombol pegas, overlay lebih lembut, efek blur latar yang berat dihapus.
- Developer: foto baru, bio, teks Tentang Jemci lebih lengkap, link TikTok/Instagram/Saluran terisi.
- Perbaikan: akses sessionStorage yang bisa menjatuhkan seluruh script di mode privat, tombol Download APK kosong kini memberi tahu, `.env.example.txt` tidak lagi berisi key.

## v1.6
- Audio lebih cepat bunyi: lagu disiapkan di latar belakang sebelum diketuk (2 lagu teratas tiap baris beranda, hasil pencarian, dan 2 lagu berikutnya di antrean), maksimal 2 konversi bersamaan, dan antrean disiapkan dibatalkan saat lagu baru diputar. Server: cadangan domain converter dicoba setelah 1 detik (sebelumnya 2), polling 150 ms, tunggu MP3 0,3 dtk. Bisa dimatikan di Pengaturan > Siapkan lagu lebih awal (hemat kuota converter).
- Beranda baru: banner 8 artis (foto artis dari YouTube Music), baris lagu artis terkenal dunia, viral Korea, Indonesia, Jepang, Filipina, Artis Pilihan (foto bulat). Playlist dan album dihapus dari beranda. Daftar artis ada di `BAN` dan `REG` di `index.html`, ganti sesuka hati.
- Service worker naik ke v2 supaya tampilan baru langsung terpakai.

## v1.7
- Beranda: banner dan Global Top 20 berisi lagu trending dunia, plus baris Trending di Korea, Indonesia, Jepang, Filipina. Daftar lagu (artis + judul) ada di `WLD`, `KR`, `ID`, `JP`, `PH` di `index.html`; tiap entri dicari di YouTube Music dan hasil pertama dipakai. Bendera dan Jelajahi Genre di beranda dihapus (genre tetap ada di Mencari).
- Developer: latar foto bergerak halus (zoom pelan + cahaya bergeser), mati kalau Kurangi animasi aktif.
- Tombol Download APK menampilkan "Coming soon!!".

## v1.8
- Trending dunia (banner dan Global Top 20) hanya lagu berbahasa Inggris. Daftar lagu Latin/K-Pop pindah ke baris sendiri.
- Beranda diperbanyak: Mulai Cepat (grid), Rilis Terbaru, Hits Pop Sepanjang Masa, Artis K-Pop/Indonesia (foto bulat), K-Pop Wave, Hip-Hop dan R&B, Latin Heat, Galau Inggris, Galau Indonesia, J-Pop dan Anime, Hits OPM, Santai dan Fokus, Semangat Olahraga. Daftarnya (`CPOP`, `CKP`, dst.) ada di `index.html`.
- Latar foto developer lebih hidup: bola cahaya melayang, zoom foto lebih jelas, dan not musik naik pelan.


## Integrasi katalog musik v3.5
- `/api/global` menggabungkan chart publik Apple Music Indonesia dan playlist Today's Top Hits Spotify, lalu mencocokkan track dengan YouTube Music agar tetap bisa diputar di Jemci.
- `/api/search` memprioritaskan artwork Apple Music jika judul/artis cocok; jika tidak, thumbnail YouTube Music tetap menjadi fallback.
- `/api/spotify?type=playlist&id=<id-atau-alias>` membaca playlist publik Spotify. Alias chart: `tophits`, `chillhits`, `rapcaviar`, `rockclassics`, `popallstars`, `kpop`, `animehits`, `allout2020s`, `allout2010s`, `allout2000s`, `allout90s`, `allout80s`.
- Background Jelajahi Genre mengambil cover playlist Spotify dengan fallback ke artwork YouTube Music/gradien sehingga kegagalan salah satu sumber tidak merusak halaman.
- Pencarian menerima URL playlist Spotify publik dan menampilkan lagu yang berhasil dicocokkan ke katalog pemutar Jemci.

Sumber chart dan artwork adalah layanan publik pihak ketiga; ketersediaan, urutan chart, dan format embed dapat berubah sewaktu-waktu. Jika salah satu layanan sedang membatasi akses, UI tetap menggunakan fallback yang tersedia.
