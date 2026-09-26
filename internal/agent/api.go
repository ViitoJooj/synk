package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ViitoJooj/synk/internal/config"
	"github.com/ViitoJooj/synk/internal/control"
	"github.com/ViitoJooj/synk/pkg/errorx"
)

// The verbs the control API answers; folders.*, blocks.* and trash.* are in
// api_sync.go.
func (a *runner) server() *control.Server {
	s := control.NewServer()
	s.Events(a.st.Subscribe, func() any { return a.st.Snapshot() })

	s.Handle("status", func(context.Context, json.RawMessage) (any, error) {
		return a.st.Snapshot(), nil
	})

	s.Handle("peers", func(context.Context, json.RawMessage) (any, error) {
		peers, err := Peers(a.ts, a.current())
		if err != nil {
			return nil, errorx.Tailscale(err)
		}
		return peers, nil
	})

	s.Handle("connect", func(_ context.Context, raw json.RawMessage) (any, error) {
		var args struct {
			Peer string `json:"peer"`
		}
		if err := decode(raw, &args); err != nil {
			return nil, err
		}
		peer, err := Choose(a.ts, args.Peer)
		if err != nil {
			return nil, err
		}
		if err := a.reload(); err != nil {
			return nil, errorx.Failed("apply the new peer", err)
		}
		return InfoOf(peer, peer.Host), nil
	})

	s.Handle("logs", func(_ context.Context, raw json.RawMessage) (any, error) {
		var args struct {
			Lines int `json:"lines"`
		}
		if err := decode(raw, &args); err != nil {
			return nil, err
		}
		return a.logs.Last(args.Lines), nil
	})

	s.Handle("reload", func(context.Context, json.RawMessage) (any, error) {
		if err := a.reload(); err != nil {
			return nil, errorx.Failed("reload the settings", err)
		}
		return a.st.Snapshot(), nil
	})

	s.Handle("config.ls", func(context.Context, json.RawMessage) (any, error) {
		cfg, err := loadSettings()
		if err != nil {
			return nil, err
		}
		return config.List(cfg), nil
	})

	s.Handle("config.set", func(_ context.Context, raw json.RawMessage) (any, error) {
		var args struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		if err := decode(raw, &args); err != nil {
			return nil, err
		}
		if !config.KeepsConfig {
			return nil, errorx.NoConfigHere()
		}
		path, err := config.Path()
		if err != nil {
			return nil, errorx.Failed("locate the config", err)
		}
		if err := config.Set(path, args.Key, args.Value); err != nil {
			return nil, errorx.BadSetting(err)
		}
		if err := a.reload(); err != nil {
			return nil, errorx.Failed("apply the setting", err)
		}
		cfg, err := loadSettings()
		if err != nil {
			return nil, err
		}
		return config.List(cfg), nil
	})

	a.syncVerbs(s)
	return s
}

// current is the peer the agent is set to link with.
func (a *runner) current() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.opts.Peer != "" {
		return a.opts.Peer
	}
	return config.Chosen(a.opts.Config)
}

func loadSettings() (*config.Config, error) {
	if !config.KeepsConfig {
		return nil, errorx.NoConfigHere()
	}
	path, err := config.Path()
	if err != nil {
		return nil, errorx.Failed("locate the config", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		return nil, errorx.NoConfig(path, err)
	}
	return cfg, nil
}

func decode(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return &errorx.Error{Code: "snk-12", Msg: "malformed arguments", Kind: errorx.User, Err: fmt.Errorf("%w", err)}
	}
	return nil
}
