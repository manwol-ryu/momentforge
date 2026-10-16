package chzzk

import (
	"context"
	"encoding/json"
	"net/url"

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
	unavailable := domain.NewUserError("PLAYBACK_UNAVAILABLE", "공개 재생 주소를 확인하지 못했습니다. 이 영상은 치지직에서 확인해 주세요.")
	raw, _ := data["liveRewindPlaybackJson"].(string)
	if raw == "" {
		return platform.Playback{}, unavailable
	}
	var pb struct {
		Meta struct {
			PaidLive         bool   `json:"paidLive"`
			PlaybackAuthType string `json:"playbackAuthType"`
		} `json:"meta"`
		Media []struct {
			Protocol string `json:"protocol"`
			Path     string `json:"path"`
		} `json:"media"`
	}
	if err := json.Unmarshal([]byte(raw), &pb); err != nil || pb.Meta.PaidLive || pb.Meta.PlaybackAuthType != "NONE" {
		return platform.Playback{}, unavailable
	}
	for _, m := range pb.Media {
		u, err := url.Parse(m.Path)
		if err != nil || m.Protocol != "HLS" || u.Scheme != "https" || u.User != nil ||
			(u.Port() != "" && u.Port() != "443") || !hasSuffix(u.Hostname(), cdnSuffix) {
			continue
		}
		return platform.Playback{URL: m.Path, Type: "hls"}, nil
	}
	return platform.Playback{}, unavailable
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
