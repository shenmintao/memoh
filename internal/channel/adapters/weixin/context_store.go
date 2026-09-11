package weixin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"strings"

	"github.com/felinics/memoh/internal/channel"
)

// SetContextTokenSaver persists per-recipient tokens in the channel store.
// Only the Channel process writes this state; Server sends use its live runtime.
func (a *WeixinAdapter) SetContextTokenSaver(save func(context.Context, string, string, string, string, string) error) {
	a.saveContext = save
}

func contextAccount(cfg channel.ChannelConfig) (string, string) {
	token, _ := cfg.Credentials["token"].(string)
	token = strings.TrimSpace(token)
	sum := sha256.Sum256([]byte(token))
	return token, hex.EncodeToString(sum[:])
}

func (a *WeixinAdapter) rememberContext(ctx context.Context, cfg channel.ChannelConfig, target, token string) {
	target = strings.TrimSpace(target)
	if target == "" || strings.TrimSpace(token) == "" {
		return
	}
	account, hash := contextAccount(cfg)
	a.contextCache.Put(cfg.ID+":"+hash+":"+target, token)
	if a.saveContext != nil {
		if err := a.saveContext(ctx, cfg.ID, target, token, hash, account); err != nil {
			a.logger.Warn("weixin context persistence failed", slog.String("config_id", cfg.ID))
		}
	}
}

func (a *WeixinAdapter) resolveContext(cfg channel.ChannelConfig, target string) (string, bool) {
	_, hash := contextAccount(cfg)
	key := cfg.ID + ":" + hash + ":" + strings.TrimSpace(target)
	if token, ok := a.contextCache.Get(key); ok {
		return token, true
	}
	tokens, _ := cfg.Routing["_weixin_contexts"].(map[string]any)
	entry, _ := tokens[strings.TrimSpace(target)].(map[string]any)
	savedHash, _ := entry["account_hash"].(string)
	token, _ := entry["token"].(string)
	if savedHash != hash || strings.TrimSpace(token) == "" {
		return "", false
	}
	a.contextCache.Put(key, token)
	return token, true
}
