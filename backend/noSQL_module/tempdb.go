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

type TempDBResponse struct {
	OrgID     platform.ID `json:"org_id"`
	OrgName   string      `json:"org_name"`
	UserName  string      `json:"username"`
	Password  string      `json:"password"`
	Token     string      `json:"token"`
	ExpiresAt string      `json:"expires_at"`
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
	if ttlMinutes <= 0 {
		ttlMinutes = 10
	}

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

	// Создать организацию
	org := &influxdb.Organization{Name: orgName}
	if err := s.OrgService.CreateOrganization(ctx, org); err != nil {
		return nil, &errors.Error{
			Msg:  "failed to create temporary organization",
			Err:  err,
			Code: errors.EInternal,
		}
	}

	// Создать пользователя
	user := &influxdb.User{Name: userName}
	if err := s.UserService.CreateUser(ctx, user); err != nil {
		s.OrgService.DeleteOrganization(ctx, org.ID)
		return nil, &errors.Error{
			Msg:  "failed to create temporary user",
			Err:  err,
			Code: errors.EInternal,
		}
	}

	// Установить пароль для пользователя
	if err := s.PasswordsService.SetPassword(ctx, user.ID, password); err != nil {
		s.UserService.DeleteUser(ctx, user.ID)
		s.OrgService.DeleteOrganization(ctx, org.ID)
		return nil, &errors.Error{
			Msg:  "failed to set password for user",
			Err:  err,
			Code: errors.EInternal,
		}
	}

	// Добавить пользователя как члена организации
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

	// Создать токен с полными правами на организацию и бакеты
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

	// Запланировать удаление через ttlMinutes
	expiresAt := time.Now().Add(time.Duration(ttlMinutes) * time.Minute)
	go func(orgID, userID platform.ID, authID platform.ID) {
		time.Sleep(time.Duration(ttlMinutes) * time.Minute)
		s.AuthService.DeleteAuthorization(ctx, authID)
		s.UserResourceMappingService.DeleteUserResourceMapping(ctx, userID, orgID)
		s.UserService.DeleteUser(ctx, userID)
		s.OrgService.DeleteOrganization(ctx, orgID)
	}(org.ID, user.ID, auth.ID)

	return &TempDBResponse{
		OrgID:     org.ID,
		OrgName:   orgName,
		UserName:  userName,
		Password:  password,
		Token:     auth.Token,
		ExpiresAt: expiresAt.Format(time.RFC3339),
	}, nil
}

func (s *TempDBService) DeleteTempDB(ctx context.Context, orgName string) error {
	// Найти организацию по имени
	orgs, _, err := s.OrgService.FindOrganizations(ctx, influxdb.OrganizationFilter{Name: &orgName})
	if err != nil || len(orgs) == 0 {
		return &errors.Error{
			Msg:  "organization not found",
			Err:  err,
			Code: errors.ENotFound,
		}
	}
	org := orgs[0]

	// Найти всех пользователей, привязанных к организации
	mappings, _, err := s.UserResourceMappingService.FindUserResourceMappings(ctx, influxdb.UserResourceMappingFilter{
		ResourceID:   org.ID,
		ResourceType: influxdb.OrgsResourceType,
	})
	if err != nil {
		return &errors.Error{
			Msg:  "failed to find user mappings",
			Err:  err,
			Code: errors.EInternal,
		}
	}

	for _, m := range mappings {

		if m.UserType != influxdb.Member {
			continue
		}

		userID := m.UserID

		// найти все авторизации этого юзера
		auths, _, _ := s.AuthService.FindAuthorizations(ctx, influxdb.AuthorizationFilter{UserID: &userID})
		for _, a := range auths {
			_ = s.AuthService.DeleteAuthorization(ctx, a.ID)
		}

		// удалить UserResourceMapping
		_ = s.UserResourceMappingService.DeleteUserResourceMapping(ctx, userID, org.ID)

		// удалить пользователя
		_ = s.UserService.DeleteUser(ctx, userID)
	}

	// удалить организацию
	if err := s.OrgService.DeleteOrganization(ctx, org.ID); err != nil {
		return &errors.Error{
			Msg:  "failed to delete organization",
			Err:  err,
			Code: errors.EInternal,
		}
	}

	return nil
}
