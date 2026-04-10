package noSQL_module

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"github.com/influxdata/influxdb/v2"
	"github.com/influxdata/influxdb/v2/kit/platform"
	"github.com/influxdata/influxdb/v2/kit/platform/errors"
	"time"
)

type TempDBService struct {
	OrgService                 influxdb.OrganizationService
	UserService                influxdb.UserService
	AuthService                influxdb.AuthorizationService
	PasswordsService           influxdb.PasswordsService
	UserResourceMappingService influxdb.UserResourceMappingService
}

type TempDBTimings struct {
	Total                     int64 `json:"total_ms"`
	GenerateUniqueData        int64 `json:"generate_unique_data_ms"`
	CreateOrganization        int64 `json:"create_organization_ms"`
	CreateUser                int64 `json:"create_user_ms"`
	SetUserPassword           int64 `json:"set_user_password_ms"`
	AddUserToOrganization     int64 `json:"add_user_to_org_ms"`
	CreateAuthorizationObject int64 `json:"create_authorization_object_ms"`
	SaveAuthorization         int64 `json:"save_authorization_ms"`
}

type DeleteDBTimings struct {
	Total                  int64 `json:"total_ms"`
	FindOrganization       int64 `json:"find_organization_ms"`
	FindUserMappings       int64 `json:"find_user_mappings_ms"`
	DeleteAuthorizations   int64 `json:"delete_authorizations_ms"`
	DeleteUserMappings     int64 `json:"delete_user_mappings_ms"`
	DeleteUsers            int64 `json:"delete_users_ms"`
	DeleteOrganization     int64 `json:"delete_organization_ms"`
}

type TempDBResponse struct {
	OrgID     platform.ID `json:"org_id"`
	OrgName   string      `json:"org_name"`
	UserName  string      `json:"username"`
	Password  string      `json:"password"`
	Token     string      `json:"token"`
	ExpiresAt string      `json:"expires_at"`
	Timings   TempDBTimings `json:"timings"`
}

// generateRandomString генерирует случайную строку длиной n байт, закодированную в base64.
func generateRandomString(n int) (string, error) {
	b := make([]byte, n)
	_, err := rand.Read(b)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b)[:n], nil
}

func (s *TempDBService) CreateTempDB(ctx context.Context, ttlMinutes int) (*TempDBResponse, error) {
    timings := TempDBTimings{}
    startTotal := time.Now()
	if ttlMinutes <= 0 {
		ttlMinutes = 10
	}
    start := time.Now()
	// Генерация уникальных данных
	orgName := fmt.Sprintf("temp_org_%d", time.Now().UnixNano())
	userName := fmt.Sprintf("temp_user_%d", time.Now().UnixNano())
	password, err := generateRandomString(10)
	if err != nil {
		return nil, &errors.Error{
			Msg:  "failed to generate password",
			Err:  err,
			Code: errors.EInternal,
		}
	}
    timings.GenerateUniqueData = time.Since(start).Milliseconds()

    start = time.Now()
	// Создать организацию
	org := &influxdb.Organization{Name: orgName}
	if err := s.OrgService.CreateOrganization(ctx, org); err != nil {
		return nil, &errors.Error{
			Msg:  "failed to create temporary organization",
			Err:  err,
			Code: errors.EInternal,
		}
	}
    timings.CreateOrganization = time.Since(start).Milliseconds()

	// Создать пользователя
	start = time.Now()
	user := &influxdb.User{Name: userName}
	if err := s.UserService.CreateUser(ctx, user); err != nil {
		s.OrgService.DeleteOrganization(ctx, org.ID)
		return nil, &errors.Error{
			Msg:  "failed to create temporary user",
			Err:  err,
			Code: errors.EInternal,
		}
	}
    timings.CreateUser = time.Since(start).Milliseconds()

	// Установить пароль для пользователя
	start = time.Now()
	if err := s.PasswordsService.SetPassword(ctx, user.ID, password); err != nil {
		s.UserService.DeleteUser(ctx, user.ID)
		s.OrgService.DeleteOrganization(ctx, org.ID)
		return nil, &errors.Error{
			Msg:  "failed to set password for user",
			Err:  err,
			Code: errors.EInternal,
		}
	}
    timings.SetUserPassword = time.Since(start).Milliseconds()

	// Добавить пользователя как члена организации
	start = time.Now()
	mapping := &influxdb.UserResourceMapping{
		UserID:       user.ID,
		ResourceID:   org.ID,
		ResourceType: influxdb.OrgsResourceType,
		UserType:     influxdb.Member,
	}
	if err := s.UserResourceMappingService.CreateUserResourceMapping(ctx, mapping); err != nil {
		s.UserService.DeleteUser(ctx, user.ID)
		s.OrgService.DeleteOrganization(ctx, org.ID)
		return nil, &errors.Error{
			Msg:  "failed to add user as member of organization",
			Err:  err,
			Code: errors.EInternal,
		}
	}
    timings.AddUserToOrganization = time.Since(start).Milliseconds()

	// Создать токен с полными правами на организацию и бакеты
	start = time.Now()
	permissions := []influxdb.Permission{
		{
			Action: influxdb.ReadAction,
			Resource: influxdb.Resource{
				Type: influxdb.OrgsResourceType,
				ID:   &org.ID,
			},
		},
		{
			Action: influxdb.WriteAction,
			Resource: influxdb.Resource{
				Type: influxdb.OrgsResourceType,
				ID:   &org.ID,
			},
		},
		{
			Action: influxdb.ReadAction,
			Resource: influxdb.Resource{
				Type:  influxdb.BucketsResourceType,
				OrgID: &org.ID,
			},
		},
		{
			Action: influxdb.WriteAction,
			Resource: influxdb.Resource{
				Type:  influxdb.BucketsResourceType,
				OrgID: &org.ID,
			},
		},
	}
	auth := &influxdb.Authorization{
		UserID:      user.ID,
		OrgID:       org.ID,
		Permissions: permissions,
		Description: "Temporary admin token for " + orgName,
	}
    timings.CreateAuthorizationObject = time.Since(start).Milliseconds()

    start = time.Now()
	if err := s.AuthService.CreateAuthorization(ctx, auth); err != nil {
		s.UserResourceMappingService.DeleteUserResourceMapping(ctx, user.ID, org.ID)
		s.UserService.DeleteUser(ctx, user.ID)
		s.OrgService.DeleteOrganization(ctx, org.ID)
		return nil, &errors.Error{
			Msg:  "failed to create temporary authorization",
			Err:  err,
			Code: errors.EInternal,
		}
	}
    timings.SaveAuthorization = time.Since(start).Milliseconds()

	// Запланировать удаление через ttlMinutes
	expiresAt := time.Now().Add(time.Duration(ttlMinutes) * time.Minute)
	go func(orgID, userID platform.ID, authID platform.ID) {
		time.Sleep(time.Duration(ttlMinutes) * time.Minute)
		s.AuthService.DeleteAuthorization(ctx, authID)
		s.UserResourceMappingService.DeleteUserResourceMapping(ctx, userID, orgID)
		s.UserService.DeleteUser(ctx, userID)
		s.OrgService.DeleteOrganization(ctx, orgID)
	}(org.ID, user.ID, auth.ID)
    timings.Total = time.Since(startTotal).Milliseconds()

	return &TempDBResponse{
		OrgID:     org.ID,
		OrgName:   orgName,
		UserName:  userName,
		Password:  password,
		Token:     auth.Token,
		ExpiresAt: expiresAt.Format(time.RFC3339),
		Timings: timings,
	}, nil
}

func (s *TempDBService) DeleteTempDB(ctx context.Context, orgName string) (*DeleteDBTimings, error) {
	timings := DeleteDBTimings{}
	startTotal := time.Now()

	// Найти организацию по имени
	start := time.Now()
	orgs, _, err := s.OrgService.FindOrganizations(ctx, influxdb.OrganizationFilter{Name: &orgName})
	timings.FindOrganization = time.Since(start).Milliseconds()
	if err != nil || len(orgs) == 0 {
		return nil, &errors.Error{
			Msg:  "organization not found",
			Err:  err,
			Code: errors.ENotFound,
		}
	}
	org := orgs[0]

	// Найти всех пользователей, привязанных к организации
	start = time.Now()
	mappings, _, err := s.UserResourceMappingService.FindUserResourceMappings(ctx, influxdb.UserResourceMappingFilter{
		ResourceID:   org.ID,
		ResourceType: influxdb.OrgsResourceType,
	})
	timings.FindUserMappings = time.Since(start).Milliseconds()
	if err != nil {
		return nil, &errors.Error{
			Msg:  "failed to find user mappings",
			Err:  err,
			Code: errors.EInternal,
		}
	}

	// Удаление пользователей и их авторизаций
	startAuth := time.Now()
	for _, m := range mappings {
		if m.UserType != influxdb.Member {
			continue
		}

		userID := m.UserID

		// удалить авторизации
		auths, _, _ := s.AuthService.FindAuthorizations(ctx, influxdb.AuthorizationFilter{UserID: &userID})
		for _, a := range auths {
			_ = s.AuthService.DeleteAuthorization(ctx, a.ID)
		}

		// удалить UserResourceMapping
		_ = s.UserResourceMappingService.DeleteUserResourceMapping(ctx, userID, org.ID)

		// удалить пользователя
		_ = s.UserService.DeleteUser(ctx, userID)
	}
	timings.DeleteAuthorizations = time.Since(startAuth).Milliseconds()
	// можно, при желании, отдельно замерить DeleteUserMappings и DeleteUsers, но чаще объединяют

	// удалить организацию
	start = time.Now()
	if err := s.OrgService.DeleteOrganization(ctx, org.ID); err != nil {
		return nil, &errors.Error{
			Msg:  "failed to delete organization",
			Err:  err,
			Code: errors.EInternal,
		}
	}
	timings.DeleteOrganization = time.Since(start).Milliseconds()

	// общее время
	timings.Total = time.Since(startTotal).Milliseconds()
	return &timings, nil
}
