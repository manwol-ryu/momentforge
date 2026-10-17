package chzzk

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"

	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/platform"
)

// Playback returns the public HLS stream of a VOD. Adult, paid and
// encrypted videos are refused. The URL is signed; callers must not store it.
func (c *Chzzk) Playback(ctx context.Context, t platform.Target) (platform.Playback, error) {
	data, _, err := c.get(ctx, "/service/v3/videos/"+t.ID, nil)
	if err != nil {
		return platform.Playback{}, err
	}
	return parsePlayback(data)
}

func parsePlayback(data map[string]any) (platform.Playback, error) {
	if truthy(data["adult"]) || truthy(data["paidProductId"]) || truthy(data["paidProduct"]) || truthy(data["encryptionType"]) {
		return platform.Playback{}, domain.NewUserError("PLAYBACK_RESTRICTED",
			"이 영상은 앱 내부 공개 재생을 지원하지 않습니다. 치지직에서 시청 권한을 확인해 주세요.")
	}
	raw, _ := data["liveRewindPlaybackJson"].(string)
	if u, ok := publicHLS(raw, "", cdnSuffix); ok {
		return platform.Playback{URL: u, Type: "hls"}, nil
	}
	return platform.Playback{}, domain.NewUserError("PLAYBACK_UNAVAILABLE", "공개 재생 주소를 확인하지 못했습니다. 이 영상은 치지직에서 확인해 주세요.")
}

// LivePlayback returns the public HLS stream of the channel's current
// broadcast, read from the same live-detail response as LiveMeta (one
// request, no retries: the screen asks again). Offline, adult and paid
// broadcasts are refused. The URL is signed; callers must not store it.
func (c *Chzzk) LivePlayback(ctx context.Context, t platform.Target) (platform.Playback, error) {
	m, err := c.liveJSON(ctx, c.base+"/service/v2/channels/"+t.ID+"/live-detail")
	if err != nil {
		return platform.Playback{}, err
	}
	content, ok := m["content"].(map[string]any)
	if fmt.Sprint(m["code"]) != "200" || !ok {
		return platform.Playback{}, liveErr("LIVE_MISSING", "방송 정보를 찾지 못했습니다. 방송이 종료되었는지 확인하세요.")
	}
	return parseLivePlayback(content)
}

func parseLivePlayback(c map[string]any) (platform.Playback, error) {
	if c["status"] != "OPEN" {
		return platform.Playback{}, domain.NewUserError("LIVE_OFFLINE", "지금은 방송 중이 아니라 영상을 불러올 수 없습니다.")
	}
	if truthy(c["adult"]) || truthy(c["paidProduct"]) || truthy(c["paidPromotion"]) {
		return platform.Playback{}, domain.NewUserError("PLAYBACK_RESTRICTED",
			"연령 제한이나 유료 방송은 앱 안에서 재생하지 않습니다. 치지직에서 시청해 주세요.")
	}
	raw, _ := c["livePlaybackJson"].(string)
	// Plain HLS rather than LLHLS: hls.js plays it without low-latency parts.
	if u, ok := publicHLS(raw, "HLS", cdnSuffix, liveCDNSuffix); ok {
		return platform.Playback{URL: u, Type: "hls"}, nil
	}
	return platform.Playback{}, domain.NewUserError("PLAYBACK_UNAVAILABLE", "방송 영상 주소를 확인하지 못했습니다. 치지직에서 시청해 주세요.")
}

// publicHLS picks an https HLS path on one of the CDN host suffixes from a
// playback JSON (VOD liveRewindPlaybackJson or livePlaybackJson). A free
// broadcast without playback authentication is required. The media whose
// mediaId is prefer wins; any other HLS media is the fallback.
func publicHLS(raw, prefer string, cdns ...string) (string, bool) {
	if raw == "" {
		return "", false
	}
	var pb struct {
		Meta struct {
			PaidLive         bool   `json:"paidLive"`
			PlaybackAuthType string `json:"playbackAuthType"`
		} `json:"meta"`
		Media []struct {
			MediaID  string `json:"mediaId"`
			Protocol string `json:"protocol"`
			Path     string `json:"path"`
		} `json:"media"`
	}
	if err := json.Unmarshal([]byte(raw), &pb); err != nil || pb.Meta.PaidLive || pb.Meta.PlaybackAuthType != "NONE" {
		return "", false
	}
	found := ""
	for _, m := range pb.Media {
		u, err := url.Parse(m.Path)
		if err != nil || m.Protocol != "HLS" || u.Scheme != "https" || u.User != nil ||
			(u.Port() != "" && u.Port() != "443") || !slices.ContainsFunc(cdns, func(s string) bool { return hasSuffix(u.Hostname(), s) }) {
			continue
		}
		if prefer == "" || m.MediaID == prefer {
			return m.Path, true
		}
		if found == "" {
			found = m.Path
		}
	}
	return found, found != ""
}

// truthy mirrors Python truthiness for JSON values.
func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case json.Number:
		return x.String() != "0" && x.String() != "0.0"
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	}
	return true
}

func hasSuffix(host, suffix string) bool {
	return len(host) > len(suffix) && host[len(host)-len(suffix):] == suffix
}
