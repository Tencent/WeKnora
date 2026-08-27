package client

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// Environment variable sources, as reported by ListMyEnvVars. The three states
// are distinct: a value the workspace filled in for everybody and one the
// caller filled in for themselves lead to different actions, and neither is
// "nobody has filled this in".
const (
	EnvSourceUnset     = "unset"
	EnvSourceWorkspace = "workspace"
	EnvSourceUser      = "user"
)

// EnvVarView is one environment variable as its owner may see it. There is
// deliberately no value field: the API never reads a stored value back out.
type EnvVarView struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
	// Source is one of EnvSourceUnset, EnvSourceWorkspace or EnvSourceUser,
	// and reports which layer an execution would actually take the value from.
	Source string `json:"source"`
	// UpdatedAt is set only for the caller's own value.
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

// SkillEnvGroup is one skill's declared credentials.
type SkillEnvGroup struct {
	SkillID   string       `json:"skill_id"`
	SkillName string       `json:"skill_name"`
	Vars      []EnvVarView `json:"vars"`
}

// ConfigEnvGroup is one sandbox config: the caller's own config-wide variables
// plus the declared credentials of the skills installed on it.
type ConfigEnvGroup struct {
	SandboxConfigID   string          `json:"sandbox_config_id"`
	SandboxConfigName string          `json:"sandbox_config_name"`
	Vars              []EnvVarView    `json:"vars"`
	Skills            []SkillEnvGroup `json:"skills"`
}

type myEnvVarsResponse struct {
	Success bool             `json:"success"`
	Data    []ConfigEnvGroup `json:"data"`
}

// envVarRequest is the body every write endpoint takes. It carries no identity:
// the server derives whose values are touched from the credentials on the
// request, so one caller can never write another's.
type envVarRequest struct {
	SkillID         string `json:"skill_id,omitempty"`
	SandboxConfigID string `json:"sandbox_config_id,omitempty"`
	Name            string `json:"name"`
	Value           string `json:"value,omitempty"`
}

// ListMyEnvVars returns every sandbox config of the workspace with the caller's
// own config-wide variables and the credentials its skills declared, each
// reporting whether it is unset, filled in workspace-wide, or filled in by the
// caller. Values are never returned.
//
// Values are stored per calling identity, not per user: a run driven by an API
// key does not see values entered through the web UI, and vice versa. Use
// SetSandboxSkillEnvValues for a credential that should apply to everybody.
func (c *Client) ListMyEnvVars(ctx context.Context) ([]ConfigEnvGroup, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/me/env-vars", nil, nil)
	if err != nil {
		return nil, err
	}
	var response myEnvVarsResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}
	return response.Data, nil
}

// SetMySkillEnvVar stores the caller's own value for one variable a skill
// declared. It overrides the workspace-wide value for this caller only, and is
// injected only into executions that name the skill. A name the skill did not
// declare is refused.
func (c *Client) SetMySkillEnvVar(ctx context.Context, skillID, name, value string) error {
	if skillID == "" {
		return fmt.Errorf("skill ID is required")
	}
	if name == "" {
		return fmt.Errorf("environment variable name is required")
	}
	body := envVarRequest{SkillID: skillID, Name: name, Value: value}
	resp, err := c.doRequest(ctx, http.MethodPut, "/api/v1/me/env-vars/skill", body, nil)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

// DeleteMySkillEnvVar removes the caller's own value. The workspace-wide value,
// if there is one, applies again afterwards. Deleting a value that was never
// set returns a 404. Clearing is a delete rather than a write of "", so there
// is one unambiguous way to revoke.
func (c *Client) DeleteMySkillEnvVar(ctx context.Context, skillID, name string) error {
	if skillID == "" {
		return fmt.Errorf("skill ID is required")
	}
	if name == "" {
		return fmt.Errorf("environment variable name is required")
	}
	body := envVarRequest{SkillID: skillID, Name: name}
	resp, err := c.doRequest(ctx, http.MethodDelete, "/api/v1/me/env-vars/skill", body, nil)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

// SetMySandboxEnvVar stores the caller's own value for one config-wide
// variable. Unlike a skill variable the name is free-form, and the value is
// injected into every skill script and shell command this caller runs on that
// sandbox config. Names starting with WEKNORA_, and names the sandbox relies on
// such as PATH, are refused.
func (c *Client) SetMySandboxEnvVar(ctx context.Context, configID, name, value string) error {
	if configID == "" {
		return fmt.Errorf("sandbox config ID is required")
	}
	if name == "" {
		return fmt.Errorf("environment variable name is required")
	}
	body := envVarRequest{SandboxConfigID: configID, Name: name, Value: value}
	resp, err := c.doRequest(ctx, http.MethodPut, "/api/v1/me/env-vars/sandbox", body, nil)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

// DeleteMySandboxEnvVar removes the caller's own config-wide variable.
func (c *Client) DeleteMySandboxEnvVar(ctx context.Context, configID, name string) error {
	if configID == "" {
		return fmt.Errorf("sandbox config ID is required")
	}
	if name == "" {
		return fmt.Errorf("environment variable name is required")
	}
	body := envVarRequest{SandboxConfigID: configID, Name: name}
	resp, err := c.doRequest(ctx, http.MethodDelete, "/api/v1/me/env-vars/sandbox", body, nil)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}
