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

	"github.com/golang-jwt/jwt"
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
	bastionUsername string

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

func NewServices(metadata *Metadata, apiKey string, kubeConfig string, cloud string, bastionUsername string, installerRsa string, baseDomain string) (*Services, error) {
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
		log.Fatalf("Error: NewServices: initCloudObjectStorageService returns %v", err)
		return nil, err
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
	}

	services = &Services{
		apiKey:          apiKey,
		kubeConfig:      kubeConfig,
		cloud:           cloud,
		bastionUsername: bastionUsername,
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

func (svc *Services) GetBastionUsername() string {
	return svc.bastionUsername
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

	if strings.HasPrefix(config.IAMAccessToken, "Bearer") {
		bluemixToken = config.IAMAccessToken[7:len(config.IAMAccessToken)]
	} else {
		bluemixToken = config.IAMAccessToken
	}

	token, err := jwt.Parse(bluemixToken, func(token *jwt.Token) (interface{}, error) {
		return "", nil
	})
	if err != nil && !strings.Contains(err.Error(), "key is of invalid type") {
		return &user, err
	}

	claims := token.Claims.(jwt.MapClaims)
	if email, ok := claims["email"]; ok {
		user.Email = email.(string)
	}
	user.ID = claims["id"].(string)
	user.Account = claims["account"].(map[string]interface{})["bss"].(string)
	iss := claims["iss"].(string)
	if strings.Contains(iss, "https://iam.cloud.ibm.com") {
		user.cloudName = "bluemix"
	} else {
		user.cloudName = "staging"
	}
	user.cloudType = "public"
	user.generation = generation

	log.Debugf("fetchUserDetails: user.ID         = %v", user.ID)
	log.Debugf("fetchUserDetails: user.Email      = %v", user.Email)
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
		log.Fatalf("Error: resourcecontrollerv2.NewResourceControllerV2 returns %v", err)
		return nil, err
	}
	if controllerSvc == nil {
		panic(fmt.Errorf("Error: controllerSvc is empty?"))
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
			err = fmt.Errorf("Error: :getCISInstanceCRN: authenticator.Validate: %v", err)
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
		if listZonesResponse == nil {
			err = fmt.Errorf("Error: getCISInstanceCRN: ListZones: %v", err)
			return
		}

		for _, zone := range listZonesResponse.Result {
			log.Debugf("getCISInstanceCRN: zone.Name = %s, zone.Status = %s", *zone.Name, *zone.Status)

			if *zone.Status == "active" {
				if *zone.Name == baseDomain {
					CISInstanceCRN = *instance.CRN
				}
			}
		}
	}

	return
}
