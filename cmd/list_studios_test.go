package cmd

import (
	"testing"

	"github.com/spf13/cobra"

	"github.com/Anastylosis/FSS/internal/config"
)

func listStudiosPath(t *testing.T, args []string, conf *string) string {
	t.Helper()
	origCfg := cfg
	t.Cleanup(func() { cfg = origCfg })
	cfg = &config.Config{DB: conf}

	c := &cobra.Command{Use: "list-studios"}
	c.Flags().String("db", "", "")
	c.Flags().Lookup("db").NoOptDefVal = "default"
	if err := c.ParseFlags(args); err != nil {
		t.Fatal(err)
	}
	return resolveDBPath(c)
}

func TestListStudiosResolvesDB(t *testing.T) {
	cases := []struct {
		name string
		args []string
		conf *string
		want string
	}{
		{"flag wins over config", []string{"--db=/from/flag.db"}, config.DBRef("/from/config.db"), "/from/flag.db"},
		{"config used when flag unset", nil, config.DBRef("/from/config.db"), "/from/config.db"},
		{"neither set", nil, nil, ""},
		{"config explicitly empty", nil, config.DBRef(""), ""},
		{"explicit empty flag opts out of config", []string{"--db="}, config.DBRef("/from/config.db"), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := listStudiosPath(t, c.args, c.conf); got != c.want {
				t.Errorf("resolved db = %q, want %q", got, c.want)
			}
		})
	}
}
