//go:build windows

package cli

import (
	"github.com/spf13/cobra"

	"github.com/ViitoJooj/synk/internal/service"
	"github.com/ViitoJooj/synk/pkg/errorx"
)

func runLogs(cmd *cobra.Command, _ []string) error {
	path, err := service.New().LogPath()
	if err != nil {
		return errorx.Failed("find the log", err)
	}

	ctx, stop := signalContext()
	defer stop()
	if err := service.FollowLog(ctx, cmd.OutOrStdout(), path, logsLines, logsFollow); err != nil {
		return errorx.Failed("read "+path, err)
	}
	return nil
}
