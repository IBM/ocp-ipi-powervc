// Copyright 2025 IBM Corp
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"fmt"
	gohttp "net/http"
	"strings"

	"github.com/IBM-Cloud/bluemix-go"
	"github.com/IBM-Cloud/bluemix-go/authentication"
	"github.com/IBM-Cloud/bluemix-go/http"
	"github.com/IBM-Cloud/bluemix-go/rest"
	bxsession "github.com/IBM-Cloud/bluemix-go/session"

	"github.com/IBM/go-sdk-core/v5/core"

	"github.com/IBM/networking-go-sdk/zonesv1"

	"github.com/IBM/platform-services-go-sdk/resourcecontrollerv2"

	"github.com/golang-jwt/jwt/v5"
)

const (
	// cisServiceID is the IBM Cloud Internet Services (CIS) service ID
	// Reference: https://cloud.ibm.com/apidocs/cis
	cisServiceID = "75874a60-cb12-11e7-948e-37ac098eb1b9"
)

type Services struct {
	//
	apiKey string

	//
	kubeConfig string

	//
	cloud string

	//
	installerRsa string

	//
	metadata *Metadata

	//
	baseDomain string

	//
	cisInstanceCRN string

	// type ResourceControllerV2
	controllerSvc *resourcecontrollerv2.ResourceControllerV2

	//
	bxSession *bxsession.Session

	//
	user *User

	//
	ctx context.Context
}

type User struct {
	ID         string
	Email      string
	Account    string
	cloudName  string
	cloudType  string
	generation int
}

func NewServices(metadata *Metadata, apiKey string, kubeConfig string, cloud string, installerRsa string, baseDomain string) (*Services, error) {
	var (
		ctx             context.Context
		controllerSvc   *resourcecontrollerv2.ResourceControllerV2
		bxSession       *bxsession.Session
		user            *User
		cisInstanceCRN  string
		services        *Services
		err             error
	)

	ctx = context.Background()

	controllerSvc, err = initCloudObjectStorageService(apiKey)
	if err != nil {
		return nil, fmt.Errorf("NewServices: initCloudObjectStorageService: %w", err)
	}
	log.Debugf("NewServices: controllerSvc = %+v", controllerSvc)

	if apiKey != "" {
		bxSession, err = InitBXService(apiKey)
		if err != nil {
			return nil, err
		}
		log.Debugf("NewServices: bxSession = %+v", bxSession)

		user, err = fetchUserDetails(bxSession, 2)
		if err != nil {
			return nil, err
		}

		cisInstanceCRN, err = getCISInstanceCRN(apiKey, controllerSvc, baseDomain)
		log.Debugf("NewServices: cisInstanceCRN = %v, err = %+v", cisInstanceCRN, err)
		if err != nil {
			return nil, fmt.Errorf("NewServices: getCISInstanceCRN: %w", err)
		} 
	}

	services = &Services{
		apiKey:          apiKey,
		kubeConfig:      kubeConfig,
		cloud:           cloud,
		controllerSvc:   controllerSvc,
		installerRsa:    installerRsa,
		metadata:        metadata,
		baseDomain:      baseDomain,
		cisInstanceCRN:  cisInstanceCRN,
		bxSession:       bxSession,
		user:            user,
		ctx:             ctx,
	}

	return services, nil
}

func (svc *Services) GetApiKey() string {
	return svc.apiKey
}

func (svc *Services) GetKubeConfig() string {
	return svc.kubeConfig
}

func (svc *Services) GetCloud() string {
	return svc.cloud
}

func (svc *Services) GetInstallerRsa() string {
	return svc.installerRsa
}

func (svc *Services) GetMetadata() *Metadata {
	return svc.metadata
}

func (svc *Services) GetBaseDomain() string {
	return svc.baseDomain
}

func (svc *Services) GetCISInstanceCRN() string {
	return svc.cisInstanceCRN
}

func (svc *Services) GetUser() *User {
	return svc.user
}

func (svc *Services) GetContextWithTimeout() (context.Context, context.CancelFunc) {
	return context.WithTimeout(svc.ctx, defaultTimeout)
}

func (svc *Services) GetControllerSvc() *resourcecontrollerv2.ResourceControllerV2 {
	return svc.controllerSvc
}

// Close releases resources held by the Services object.
// This method should be called when the Services object is no longer needed
// to ensure proper cleanup of resources.
func (svc *Services) Close() error {
	if svc == nil {
		return nil
	}

	log.Debugf("Closing Services resources")

	// Note: The Services struct currently holds references to:
	// - controllerSvc: IBM Cloud Resource Controller service client
	// - bxSession: IBM Cloud Bluemix session
	// - ctx: context (managed by caller)
	//
	// These SDK clients don't expose explicit Close methods, but we log
	// the cleanup for debugging purposes. If future SDK versions add
	// cleanup methods, they should be called here.

	log.Debugf("Services resources closed successfully")
	return nil
}

func InitBXService(apiKey string) (*bxsession.Session, error) {
	var (
		bxSession             *bxsession.Session
		tokenProviderEndpoint string = "https://iam.cloud.ibm.com"
		err                   error
	)

	bxSession, err = bxsession.New(&bluemix.Config{
		BluemixAPIKey:         apiKey,
		TokenProviderEndpoint: &tokenProviderEndpoint,
		Debug:                 false,
	})
	if err != nil {
		return nil, fmt.Errorf("Error bxsession.New: %v", err)
	}
	log.Debugf("InitBXService: bxSession = %v", bxSession)

	tokenRefresher, err := authentication.NewIAMAuthRepository(bxSession.Config, &rest.Client{
		DefaultHeader: gohttp.Header{
			"User-Agent": []string{http.UserAgent()},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("Error authentication.NewIAMAuthRepository: %v", err)
	}
	log.Debugf("InitBXService: tokenRefresher = %v", tokenRefresher)

	err = tokenRefresher.AuthenticateAPIKey(bxSession.Config.BluemixAPIKey)
	if err != nil {
		return nil, fmt.Errorf("Error tokenRefresher.AuthenticateAPIKey: %v", err)
	}

	return bxSession, nil
}

func fetchUserDetails(bxSession *bxsession.Session, generation int) (*User, error) {
	var (
		bluemixToken string
	)

	config := bxSession.Config
	user := User{}

	if len(config.IAMAccessToken) == 0 {
		return nil, fmt.Errorf("fetchUserDetails config.IAMAccessToken is empty")
	}

	bluemixToken = strings.TrimPrefix(config.IAMAccessToken, "Bearer ")
	if bluemixToken == config.IAMAccessToken {
		// No "Bearer " prefix found, try without space
		bluemixToken = strings.TrimPrefix(config.IAMAccessToken, "Bearer")
	}

	token, _, err := jwt.NewParser().ParseUnverified(bluemixToken, jwt.MapClaims{})
	if err != nil {
		return nil, fmt.Errorf("fetchUserDetails: jwt.ParseUnverified: %w", err)
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, fmt.Errorf("fetchUserDetails: unexpected claims type")
	}
	if email, ok := claims["email"].(string); ok {
		user.Email = email
	}
	id, ok := claims["id"].(string)
	if !ok {
		return nil, fmt.Errorf("fetchUserDetails: missing or invalid 'id' claim")
	}
	user.ID = id
	account, ok := claims["account"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("fetchUserDetails: missing or invalid 'account' claim")
	}
	bss, ok := account["bss"].(string)
	if !ok {
		return nil, fmt.Errorf("fetchUserDetails: missing or invalid 'bss' in account claim")
	}
	user.Account = bss
	iss, ok := claims["iss"].(string)
	if !ok {
		return nil, fmt.Errorf("fetchUserDetails: missing or invalid 'iss' claim")
	}
	if strings.Contains(iss, "https://iam.cloud.ibm.com") {
		user.cloudName = "bluemix"
	} else {
		user.cloudName = "staging"
	}
	user.cloudType = "public"
	user.generation = generation

	log.Debugf("fetchUserDetails: user.ID         = %v", user.ID)
	// Avoid logging email for privacy compliance
	log.Debugf("fetchUserDetails: user.Account    = %v", user.Account)
	log.Debugf("fetchUserDetails: user.cloudType  = %v", user.cloudType)
	log.Debugf("fetchUserDetails: user.generation = %v", user.generation)

	return &user, nil
}

func initCloudObjectStorageService(apiKey string) (*resourcecontrollerv2.ResourceControllerV2, error) {
	var (
		authenticator core.Authenticator = &core.IamAuthenticator{
			ApiKey: apiKey,
		}
		controllerSvc *resourcecontrollerv2.ResourceControllerV2
		err           error
	)

	if apiKey == "" {
		return nil, nil
	}

	controllerSvc, err = resourcecontrollerv2.NewResourceControllerV2(&resourcecontrollerv2.ResourceControllerV2Options{
		Authenticator: authenticator,
	})
	if err != nil {
		return nil, fmt.Errorf("resourcecontrollerv2.NewResourceControllerV2: %w", err)
	}
	if controllerSvc == nil {
		return nil, fmt.Errorf("Error: controllerSvc is empty?")
	}

	return controllerSvc, nil
}

func getCISInstanceCRN(apiKey string, controllerSvc *resourcecontrollerv2.ResourceControllerV2, baseDomain string) (CISInstanceCRN string, err error) {
	var (
		listInstanceOptions           *resourcecontrollerv2.ListResourceInstancesOptions
		listResourceInstancesResponse *resourcecontrollerv2.ResourceInstancesList
		authenticator                 core.Authenticator
		instance                      resourcecontrollerv2.ResourceInstance
		zonesService                  *zonesv1.ZonesV1
		listZonesOptions              *zonesv1.ListZonesOptions
		listZonesResponse             *zonesv1.ListZonesResp
	)

	if controllerSvc == nil {
		err = fmt.Errorf("Error: getCISInstanceCRN: controllerSvc is nil")
		return
	}

	listInstanceOptions = controllerSvc.NewListResourceInstancesOptions()
	listInstanceOptions.SetResourceID(cisServiceID)
	log.Debugf("getCISInstanceCRN: listInstanceOptions = %+v", listInstanceOptions)

	listResourceInstancesResponse, _, err = controllerSvc.ListResourceInstances(listInstanceOptions)
	if err != nil {
		err = fmt.Errorf("Error: getCISInstanceCRN: ListResourceInstances: returns %v", err)
		return
	}

	for _, instance = range listResourceInstancesResponse.Resources {
		log.Debugf("getCISInstanceCRN: instance = %+v", instance)

		authenticator = &core.IamAuthenticator{
			ApiKey: apiKey,
		}
		err = authenticator.Validate()
		if err != nil {
			err = fmt.Errorf("Error: getCISInstanceCRN: authenticator.Validate: %v", err)
			return
		}

		zonesService, err = zonesv1.NewZonesV1(&zonesv1.ZonesV1Options{
			Authenticator: authenticator,
			Crn:           instance.CRN,
		})
		if err != nil {
			err = fmt.Errorf("Error: getCISInstanceCRN: NewZonesV1: %v", err)
			return
		}

		listZonesOptions = zonesService.NewListZonesOptions()

		listZonesResponse, _, err = zonesService.ListZones(listZonesOptions)
		if err != nil {
			err = fmt.Errorf("Error: getCISInstanceCRN: ListZones: %w", err)
			return
		}
		if listZonesResponse == nil || listZonesResponse.Result == nil {
			err = fmt.Errorf("Error: getCISInstanceCRN: ListZones or listZonesResponse.Result is nil")
			return
		}

		for _, zone := range listZonesResponse.Result {
			if zone.Name == nil || zone.Status == nil {
				continue
			}

			log.Debugf("getCISInstanceCRN: zone.Name = %s, zone.Status = %s", *zone.Name, *zone.Status)

			if *zone.Status == "active" {
				if *zone.Name == baseDomain {
					CISInstanceCRN = *instance.CRN
					return CISInstanceCRN, nil
				}
			}
		}
	}

	return "", fmt.Errorf("getCISInstanceCRN: no CIS instance found for domain %q", baseDomain)
}
