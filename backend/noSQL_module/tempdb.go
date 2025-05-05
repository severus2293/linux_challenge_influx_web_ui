package noSQL_module

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/influxdata/influxdb/v2"
	"github.com/influxdata/influxdb/v2/kit/platform"
	"github.com/influxdata/influxdb/v2/kit/platform/errors"
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

func (s *TempDBService) CreateTempDB(ctx context.Context) (*TempDBResponse, error) {
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

	// Запланировать удаление через 10 минут
	expiresAt := time.Now().Add(10 * time.Minute)
	go func() {
		time.Sleep(10 * time.Minute)
		s.AuthService.DeleteAuthorization(ctx, auth.ID)
		s.UserResourceMappingService.DeleteUserResourceMapping(ctx, user.ID, org.ID)
		s.UserService.DeleteUser(ctx, user.ID)
		s.OrgService.DeleteOrganization(ctx, org.ID)
	}()

	return &TempDBResponse{
		OrgID:     org.ID,
		OrgName:   orgName,
		UserName:  userName,
		Password:  password,
		Token:     auth.Token,
		ExpiresAt: expiresAt.Format(time.RFC3339),
	}, nil
}
