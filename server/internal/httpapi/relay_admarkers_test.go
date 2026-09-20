package httpapi

import (
	"net/url"
	"testing"
)

func TestScanAdMarkers(t *testing.T) {
	base, _ := url.Parse("https://cdn.example.com/live/ch1/index.m3u8")
	body := []byte(`#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:6
#EXT-X-MEDIA-SEQUENCE:120
#EXT-X-DISCONTINUITY-SEQUENCE:3
#EXTINF:6.0,
seg120.ts
#EXT-X-CUE-OUT:60
#EXT-X-DISCONTINUITY
#EXTINF:6.0,
https://ads.provider.net/spot/1.ts
#EXTINF:6.0,
https://ads.provider.net/spot/2.ts
#EXT-X-CUE-IN
#EXT-X-DISCONTINUITY
#EXT-X-DATERANGE:ID="splice-1",CLASS="com.apple.hls.interstitial"
#EXTINF:6.0,
seg121.ts
`)
	m := scanAdMarkers(body, base)
	if m.discontinuity != 2 || m.cueOut != 1 || m.cueIn != 1 || m.daterange != 1 || m.foreignHost != 2 {
		t.Fatalf("markers: %+v", m)
	}
	if clean := scanAdMarkers([]byte("#EXTM3U\n#EXTINF:6.0,\nseg1.ts\n"), base); clean != (adMarkers{}) {
		t.Fatalf("a plain playlist must count nothing: %+v", clean)
	}
	if channelOf("&ua=x&c=Suspilne.ua") != "Suspilne.ua" || channelOf("") != "" {
		t.Fatal("channelOf")
	}
}
