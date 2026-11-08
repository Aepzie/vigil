// Package deps pins third-party modules in go.mod while features are
// developed on parallel branches. It is removed once all features land.
package deps

import (
	_ "charm.land/bubbles/v2/textinput"
	_ "charm.land/log/v2"
	_ "charm.land/ssh"
	_ "charm.land/wish/v2"
	_ "charm.land/wish/v2/activeterm"
	_ "charm.land/wish/v2/bubbletea"
	_ "charm.land/wish/v2/logging"
	_ "charm.land/wish/v2/ratelimiter"
	_ "charm.land/wish/v2/recover"
	_ "github.com/charmbracelet/colorprofile"
	_ "github.com/charmbracelet/x/term"
	_ "golang.org/x/crypto/bcrypt"
	_ "golang.org/x/crypto/ssh"
	_ "golang.org/x/crypto/ssh/agent"
	_ "golang.org/x/crypto/ssh/knownhosts"
	_ "golang.org/x/sync/errgroup"
)
