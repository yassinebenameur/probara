// Package builtin is a barrel package: importing it for side-effects
// (`import _ ".../shared/notifications/plugin/builtin"`) registers every
// first-party plugin via their init() functions. Each binary that dispatches
// notifications — alerter, worker, and api — must blank-import this package
// at startup.
package builtin

import (
	_ "github.com/yassinebenameur/probara/shared/notifications/plugin/builtin/discord"
	_ "github.com/yassinebenameur/probara/shared/notifications/plugin/builtin/email"
	_ "github.com/yassinebenameur/probara/shared/notifications/plugin/builtin/slack"
	_ "github.com/yassinebenameur/probara/shared/notifications/plugin/builtin/teams"
	_ "github.com/yassinebenameur/probara/shared/notifications/plugin/builtin/webhook"
)
