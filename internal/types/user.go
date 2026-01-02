package types

import (
	"time"

	"gorm.io/gorm"
)

// User 시스템의 사용자를 나타냅니다.
type User struct {
	// 사용자의 고유 식별자
	ID string `json:"id"         gorm:"type:varchar(36);primaryKey"`
	// 사용자 이름
	Username string `json:"username"   gorm:"type:varchar(100);uniqueIndex;not null"`
	// 사용자 이메일 주소
	Email string `json:"email"      gorm:"type:varchar(255);uniqueIndex;not null"`
	// 해시된 비밀번호
	PasswordHash string `json:"-"          gorm:"type:varchar(255);not null"`
	// 사용자 아바타 URL
	Avatar string `json:"avatar"     gorm:"type:varchar(500)"`
	// 사용자가 속한 테넌트 ID
	TenantID uint64 `json:"tenant_id"  gorm:"index"`
	// 사용자 활성 상태 여부
	IsActive bool `json:"is_active"  gorm:"default:true"`
	// 사용자가 모든 테넌트에 접근할 수 있는지 여부 (크로스 테넌트 접근)
	CanAccessAllTenants bool `json:"can_access_all_tenants" gorm:"default:false"`
	// 사용자 생성 시간
	CreatedAt time.Time `json:"created_at"`
	// 사용자 마지막 업데이트 시간
	UpdatedAt time.Time `json:"updated_at"`
	// 사용자 삭제 시간
	DeletedAt gorm.DeletedAt `json:"deleted_at" gorm:"index"`

	// 연관 관계, 데이터베이스에 저장되지 않음
	Tenant *Tenant `json:"tenant,omitempty" gorm:"foreignKey:TenantID"`
}

// AuthToken 인증 토큰을 나타냅니다.
type AuthToken struct {
	// 토큰의 고유 식별자
	ID string `json:"id"         gorm:"type:varchar(36);primaryKey"`
	// 이 토큰을 소유한 사용자 ID
	UserID string `json:"user_id"    gorm:"type:varchar(36);index;not null"`
	// 토큰 값 (JWT 또는 기타 형식)
	Token string `json:"token"      gorm:"type:text;not null"`
	// 토큰 유형 (access_token, refresh_token)
	TokenType string `json:"token_type" gorm:"type:varchar(50);not null"`
	// 토큰 만료 시간
	ExpiresAt time.Time `json:"expires_at"`
	// 토큰 취소 여부
	IsRevoked bool `json:"is_revoked" gorm:"default:false"`
	// 토큰 생성 시간
	CreatedAt time.Time `json:"created_at"`
	// 토큰 마지막 업데이트 시간
	UpdatedAt time.Time `json:"updated_at"`

	// 연관 관계
	User *User `json:"user,omitempty" gorm:"foreignKey:UserID"`
}

// LoginRequest 로그인 요청을 나타냅니다.
type LoginRequest struct {
	Email    string `json:"email"    binding:"required,email"`
	Password string `json:"password" binding:"required,min=6"`
}

// RegisterRequest 등록 요청을 나타냅니다.
type RegisterRequest struct {
	Username string `json:"username" binding:"required,min=3,max=50"`
	Email    string `json:"email"    binding:"required,email"`
	Password string `json:"password" binding:"required,min=6"`
}

// LoginResponse 로그인 응답을 나타냅니다.
type LoginResponse struct {
	Success      bool    `json:"success"`
	Message      string  `json:"message,omitempty"`
	User         *User   `json:"user,omitempty"`
	Tenant       *Tenant `json:"tenant,omitempty"`
	Token        string  `json:"token,omitempty"`
	RefreshToken string  `json:"refresh_token,omitempty"`
}

// RegisterResponse 등록 응답을 나타냅니다.
type RegisterResponse struct {
	Success bool    `json:"success"`
	Message string  `json:"message,omitempty"`
	User    *User   `json:"user,omitempty"`
	Tenant  *Tenant `json:"tenant,omitempty"`
}

// UserInfo API 응답을 위한 사용자 정보를 나타냅니다.
type UserInfo struct {
	ID                  string    `json:"id"`
	Username            string    `json:"username"`
	Email               string    `json:"email"`
	Avatar              string    `json:"avatar"`
	TenantID            uint64    `json:"tenant_id"`
	IsActive            bool      `json:"is_active"`
	CanAccessAllTenants bool      `json:"can_access_all_tenants"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// ToUserInfo User를 UserInfo로 변환합니다 (민감한 데이터 제외).
func (u *User) ToUserInfo() *UserInfo {
	return &UserInfo{
		ID:                  u.ID,
		Username:            u.Username,
		Email:               u.Email,
		Avatar:              u.Avatar,
		TenantID:            u.TenantID,
		IsActive:            u.IsActive,
		CanAccessAllTenants: u.CanAccessAllTenants,
		CreatedAt:           u.CreatedAt,
		UpdatedAt:           u.UpdatedAt,
	}
}
