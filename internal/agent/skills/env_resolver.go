package skills

import (
	"context"
	"fmt"
	"strings"
)

// SkillEnvResolver produces the environment one execution gets. It is separate
// from SkillSource because the values are per-caller: the same skill, in the
// same image, hands a different key to a different Principal. The implementation
// lives in the service layer, which can reach the repository.
//
// It is keyed by skill NAME rather than id because every path into the manager
// is name-addressed: ExecuteScript receives the name the model wrote, and the
// row id is an implementation detail of the installed-skill source.
type SkillEnvResolver interface {
	// ResolveEnv returns the values to inject and the names of any required
	// variable that neither the admin nor this caller has filled in. An empty
	// skillName asks for the caller's config-wide variables alone. The caller's
	// identity is taken from ctx, never from a parameter.
	ResolveEnv(ctx context.Context, skillName string) (env map[string]string, missing []string, err error)
}

// MissingSkillEnvError reports that execution was refused because a required
// variable has no value at either layer. It is typed so the agent loop can
// relay a sentence a person can act on instead of the KeyError or 401 the
// script would otherwise produce.
type MissingSkillEnvError struct {
	SkillName string
	Names     []string
}

func (e *MissingSkillEnvError) Error() string {
	// English, like every other error in this codebase: the agent relays this
	// to the user and translates it into whatever language they are speaking.
	// execute_skill_script has no env parameter, so it cannot take a value the
	// user just typed. shell_exec can: naming the skill and passing the value
	// in env runs the command and stores the value for the next run. Pointing
	// at the settings page alone would strand IM users, who have no such page.
	return fmt.Sprintf(
		"skill %q needs the environment variable(s) %s, which nobody has set yet. "+
			"Ask the user for them, then run the skill through shell_exec with "+
			"skill_name=%q and the values in env — they are stored for that user "+
			"afterwards. They can also be set under Settings → Environment variables.",
		e.SkillName, strings.Join(e.Names, ", "), e.SkillName,
	)
}

// applyResolvedEnv overlays resolved onto env WITHOUT displacing anything env
// already carries.
//
// This is the second layer of reserved-name protection. Task 2's write-time
// blacklist is the first, but a value written before that blacklist existed
// would still be in the database, and letting it land on
// WEKNORA_SKILL_OUTPUT_DIR would silently redirect the turn's artifacts to a
// directory nobody drains. Skipping existing keys makes that impossible
// regardless of what is stored.
func applyResolvedEnv(env, resolved map[string]string) {
	for name, value := range resolved {
		if _, taken := env[name]; taken {
			continue
		}
		env[name] = value
	}
}
