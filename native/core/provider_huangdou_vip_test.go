package core

import "testing"

// TestHuangdouCoinEpisodesRequireAuthorization 验证「Huangdou Coin Episodes Require Authorization」回归场景。
func TestHuangdouCoinEpisodesRequireAuthorization(t *testing.T) {
	detail := map[string]any{"pay_type": "money", "free_episodes": "1", "episode_count": "7"}
	if flag := huangdouVIPFlag(detail); flag == nil || !*flag {
		t.Fatal("money title marked unrestricted")
	}
	if huangdouEpisodeVIP(detail, map[string]any{"type": "free", "price": 0}, 1) {
		t.Fatal("free first episode blocked")
	}
	for number := 2; number <= 7; number++ {
		if !huangdouEpisodeVIP(detail, map[string]any{"type": "coin", "price": 38}, number) {
			t.Fatal("coin episode marked free", number)
		}
		if !huangdouEpisodeVIP(detail, nil, number) {
			t.Fatal("money free-episode boundary ignored", number)
		}
	}
}

// TestHuangdouEpisodeFreeFlagAndUnknownMetadata 验证「Huangdou Episode Free Flag And Unknown Metadata」回归场景。
func TestHuangdouEpisodeFreeFlagAndUnknownMetadata(t *testing.T) {
	if huangdouEpisodeVIP(nil, nil, 1) {
		t.Fatal("unknown metadata was invented as paid")
	}
	if huangdouEpisodeVIP(map[string]any{"pay_type": "money", "free_episodes": 1}, map[string]any{"type": "free"}, 2) {
		t.Fatal("explicit free episode flag ignored")
	}
}
