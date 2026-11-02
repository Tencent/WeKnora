package credential

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
)

// Owner is whose credentials a call touches. It always comes from the
// authenticated context, never from a request body.
type Owner struct {
	TenantID uint64
	UserID   string
}

// OwnerFromContext returns the signed-in web user acting in person. Embed
// visitors, API keys, IM users and automations own no vault.
func OwnerFromContext(ctx context.Context) (Owner, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return Owner{}, apperrors.NewUnauthorizedError("workspace context is required")
	}
	p, ok := types.PrincipalFromContext(ctx)
	if !ok || p.Type != types.PrincipalWebUser || p.ActingVia != "" {
		return Owner{}, apperrors.NewForbiddenError("only a signed-in user can use saved credentials")
	}
	return Owner{TenantID: tenantID, UserID: p.StorageID()}, nil
}

// Available reports whether secrets can be stored. EncryptAESGCM returns its
// input unchanged without a key, so nothing here may run without one.
func Available() bool { return utils.GetAESKey() != nil }

func errUnavailable() error {
	return apperrors.NewServiceUnavailableError("saved credentials are not enabled on this server")
}

func badRequest(err error) error { return apperrors.NewBadRequestError(err.Error()) }

// LoginInput is a login to store.
type LoginInput struct {
	Title    string
	Site     string
	Domains  []string
	Username string
	Password string
}

// LoginPatch changes some fields of a saved login. Nil leaves a field as is;
// a non-nil empty Domains resets the list to the site alone.
type LoginPatch struct {
	Title    *string
	Domains  []string
	Username *string
	Password *string
}

// Vault stores logins for their owner.
type Vault struct {
	repo     interfaces.CredentialVaultRepository
	now      func() time.Time
	maxItems int
}

// NewVault builds a vault over repo.
func NewVault(repo interfaces.CredentialVaultRepository) *Vault {
	return &Vault{repo: repo, now: time.Now, maxItems: types.CredentialVaultMaxItems}
}

// sealed binds the password to its row so a ciphertext copied onto another
// row does not open.
type sealed struct {
	ItemID   string `json:"item_id"`
	UserID   string `json:"user_id"`
	Password string `json:"password"`
}

func seal(itemID, userID, password string) (string, error) {
	key := utils.GetAESKey()
	if key == nil {
		return "", errUnavailable()
	}
	raw, err := json.Marshal(sealed{ItemID: itemID, UserID: userID, Password: password})
	if err != nil {
		return "", err
	}
	return utils.EncryptAESGCM(string(raw), key)
}

func open(item *types.CredentialVaultItem) (string, error) {
	key := utils.GetAESKey()
	if key == nil || !strings.HasPrefix(item.SecretCiphertext, utils.EncPrefix) {
		return "", types.ErrCredentialUnavailable
	}
	raw, err := utils.DecryptAESGCM(item.SecretCiphertext, key)
	if err != nil {
		return "", types.ErrCredentialUnavailable
	}
	var s sealed
	if json.Unmarshal([]byte(raw), &s) != nil || s.ItemID != item.ID || s.UserID != item.UserID {
		return "", types.ErrCredentialUnavailable
	}
	return s.Password, nil
}

func normalizeTitle(title, site string) (string, error) {
	t := strings.TrimSpace(title)
	if t == "" {
		t = site
	}
	if utf8.RuneCountInString(t) > types.CredentialVaultMaxTitleLen {
		return "", apperrors.NewBadRequestError(
			fmt.Sprintf("title exceeds %d characters", types.CredentialVaultMaxTitleLen))
	}
	return t, nil
}

func normalizeUsername(username string) (string, error) {
	u := strings.TrimSpace(username)
	if utf8.RuneCountInString(u) > types.SecureFormMaxUsernameLen {
		return "", apperrors.NewBadRequestError(
			fmt.Sprintf("username exceeds %d characters", types.SecureFormMaxUsernameLen))
	}
	return u, nil
}

func checkPassword(password string) error {
	if password == "" {
		return apperrors.NewBadRequestError("password is required")
	}
	if len(password) > types.SecureFormMaxValueBytes {
		return apperrors.NewBadRequestError(fmt.Sprintf("password exceeds %d bytes", types.SecureFormMaxValueBytes))
	}
	return nil
}

func mapRepoErr(err error) error {
	switch {
	case errors.Is(err, types.ErrCredentialConflict):
		return apperrors.NewConflictError("a saved credential for this site and username already exists")
	case errors.Is(err, types.ErrCredentialNotFound):
		return apperrors.NewNotFoundError("saved credential not found")
	}
	return err
}

// List returns the owner's saved logins, optionally for one site.
func (v *Vault) List(ctx context.Context, owner Owner, site string) ([]*types.CredentialVaultItem, error) {
	if site != "" {
		s, err := types.NormalizeHost(site)
		if err != nil {
			return nil, badRequest(err)
		}
		site = s
	}
	items, err := v.repo.List(ctx, owner.TenantID, owner.UserID, site)
	if err != nil {
		return nil, err
	}
	if items == nil {
		items = []*types.CredentialVaultItem{}
	}
	return items, nil
}

// Get returns one saved login of the owner.
func (v *Vault) Get(ctx context.Context, owner Owner, id string) (*types.CredentialVaultItem, error) {
	item, err := v.repo.Get(ctx, owner.TenantID, owner.UserID, strings.TrimSpace(id))
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, apperrors.NewNotFoundError("saved credential not found")
	}
	return item, nil
}

// Create stores a new login.
func (v *Vault) Create(ctx context.Context, owner Owner, in LoginInput) (*types.CredentialVaultItem, error) {
	if !Available() {
		return nil, errUnavailable()
	}
	site, err := types.NormalizeHost(in.Site)
	if err != nil {
		return nil, badRequest(err)
	}
	domains, err := types.NormalizeDomains(site, in.Domains)
	if err != nil {
		return nil, badRequest(err)
	}
	title, err := normalizeTitle(in.Title, site)
	if err != nil {
		return nil, err
	}
	username, err := normalizeUsername(in.Username)
	if err != nil {
		return nil, err
	}
	if err := checkPassword(in.Password); err != nil {
		return nil, err
	}
	n, err := v.repo.Count(ctx, owner.TenantID, owner.UserID)
	if err != nil {
		return nil, err
	}
	if n >= int64(v.maxItems) {
		return nil, apperrors.NewBadRequestError(fmt.Sprintf("at most %d saved credentials", v.maxItems))
	}
	id := uuid.NewString()
	ciphertext, err := seal(id, owner.UserID, in.Password)
	if err != nil {
		return nil, err
	}
	now := v.now()
	item := &types.CredentialVaultItem{
		ID: id, TenantID: owner.TenantID, UserID: owner.UserID, Kind: types.CredentialKindLogin,
		Title: title, Site: site, Domains: types.StringArray(domains), Username: username,
		SecretCiphertext: ciphertext, CreatedAt: now, UpdatedAt: now,
	}
	if err := v.repo.Create(ctx, item); err != nil {
		return nil, mapRepoErr(err)
	}
	return item, nil
}

// Upsert stores a login, replacing only the password of the owner's existing
// login for the same site and username. Its domains stay as they are: the
// card's domains come from the agent, and only the vault settings widen them.
func (v *Vault) Upsert(ctx context.Context, owner Owner, in LoginInput) (*types.CredentialVaultItem, error) {
	if !Available() {
		return nil, errUnavailable()
	}
	site, err := types.NormalizeHost(in.Site)
	if err != nil {
		return nil, badRequest(err)
	}
	username, err := normalizeUsername(in.Username)
	if err != nil {
		return nil, err
	}
	existing, err := v.repo.FindBySiteUsername(ctx, owner.TenantID, owner.UserID, site, username)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return v.Create(ctx, owner, in)
	}
	if err := checkPassword(in.Password); err != nil {
		return nil, err
	}
	ciphertext, err := seal(existing.ID, owner.UserID, in.Password)
	if err != nil {
		return nil, err
	}
	existing.SecretCiphertext = ciphertext
	existing.UpdatedAt = v.now()
	if err := v.repo.Update(ctx, existing); err != nil {
		return nil, mapRepoErr(err)
	}
	return existing, nil
}

// Update applies a patch to a saved login.
func (v *Vault) Update(ctx context.Context, owner Owner, id string, p LoginPatch) (*types.CredentialVaultItem, error) {
	if !Available() {
		return nil, errUnavailable()
	}
	item, err := v.Get(ctx, owner, id)
	if err != nil {
		return nil, err
	}
	if p.Title != nil {
		if item.Title, err = normalizeTitle(*p.Title, item.Site); err != nil {
			return nil, err
		}
	}
	if p.Domains != nil {
		domains, err := types.NormalizeDomains(item.Site, p.Domains)
		if err != nil {
			return nil, badRequest(err)
		}
		item.Domains = types.StringArray(domains)
	}
	if p.Username != nil {
		if item.Username, err = normalizeUsername(*p.Username); err != nil {
			return nil, err
		}
	}
	if p.Password != nil {
		if err := checkPassword(*p.Password); err != nil {
			return nil, err
		}
		if item.SecretCiphertext, err = seal(item.ID, owner.UserID, *p.Password); err != nil {
			return nil, err
		}
	}
	item.UpdatedAt = v.now()
	if err := v.repo.Update(ctx, item); err != nil {
		return nil, mapRepoErr(err)
	}
	return item, nil
}

// Delete removes a saved login.
func (v *Vault) Delete(ctx context.Context, owner Owner, id string) error {
	ok, err := v.repo.Delete(ctx, owner.TenantID, owner.UserID, strings.TrimSpace(id))
	if err != nil {
		return err
	}
	if !ok {
		return apperrors.NewNotFoundError("saved credential not found")
	}
	return nil
}

// Password opens a saved login. Any failure reads as ErrCredentialUnavailable
// so the caller cannot tell a deleted row from a rotated key.
func (v *Vault) Password(ctx context.Context, owner Owner, id string) (*types.CredentialVaultItem, string, error) {
	item, err := v.repo.Get(ctx, owner.TenantID, owner.UserID, id)
	if err != nil || item == nil {
		return nil, "", types.ErrCredentialUnavailable
	}
	pw, err := open(item)
	if err != nil {
		return nil, "", err
	}
	return item, pw, nil
}

// Touch records that a saved login was just filled.
func (v *Vault) Touch(ctx context.Context, owner Owner, id string) error {
	return v.repo.Touch(ctx, owner.TenantID, owner.UserID, id, v.now())
}
