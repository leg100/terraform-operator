package workspace

import (
	"bytes"
	"context"
	"testing"

	cmdutil "github.com/leg100/etok/cmd/util"
	"github.com/leg100/etok/pkg/env"
	"github.com/leg100/etok/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceSelect(t *testing.T) {
	tests := []struct {
		name string
		args []string
		env  env.EtokEnv
		out  string
		err  bool
	}{
		{
			name: "defaults",
			args: []string{"dev/networking"},
			env:  env.EtokEnv("dev/networking"),
			out:  "Current workspace now: dev/networking\n",
		},
	}

	for _, tt := range tests {
		testutil.Run(t, tt.name, func(t *testutil.T) {
			path := t.NewTempDir().Chdir().Root()

			out := new(bytes.Buffer)

			opts, err := cmdutil.NewFakeOpts(out)
			require.NoError(t, err)

			cmd := SelectCmd(opts)
			cmd.SetArgs(tt.args)
			cmd.SetOut(opts.Out)

			t.CheckError(tt.err, cmd.ExecuteContext(context.Background()))

			assert.Equal(t, tt.out, out.String())

			// Confirm .terraform/environment was written with expected contents
			etokenv, err := env.ReadEtokEnv(path)
			require.NoError(t, err)
			assert.Equal(t, tt.env, etokenv)
		})
	}
}
