package store

import (
	"context"

	"github.com/whitedns/vless-tester/internal/model"
)

// SpeedSettings returns the speed-test config pushed to workers, read from the
// speed.* settings with sensible fallbacks. download_mb (>0) overrides bytes.
func (s *Store) SpeedSettings(ctx context.Context) (model.SpeedSpec, error) {
	adaptive := true
	spec := model.SpeedSpec{Streams: 6, Bytes: 10_000_000, TimeoutMs: 30000, Adaptive: &adaptive}

	_ = s.GetSetting(ctx, "speed.download_url", &spec.DownloadURL)
	_ = s.GetSetting(ctx, "speed.upload_url", &spec.UploadURL)
	_ = s.GetSetting(ctx, "speed.streams", &spec.Streams)
	_ = s.GetSetting(ctx, "speed.bytes", &spec.Bytes)
	if mb := 0; s.GetSetting(ctx, "speed.download_mb", &mb) == nil && mb > 0 {
		spec.Bytes = mb * 1_000_000
	}
	if ad := false; s.GetSetting(ctx, "speed.adaptive", &ad) == nil {
		spec.Adaptive = &ad
	}
	_ = s.GetSetting(ctx, "speed.timeout_ms", &spec.TimeoutMs)
	return spec, nil
}
