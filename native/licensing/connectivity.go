package licensing

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

const ServiceOrigin = "https://license-server.star-andy-jack.workers.dev"

// PublicNetworkCheck 检查配置的许可证服务公共端点是否可达。
func PublicNetworkCheck(version, distributionKey, serverKey string) error {
	client := defaultLicenseClient()
	defer client.CloseIdleConnections()
	r, e := http.NewRequest(http.MethodGet, ServiceOrigin+"/api/guoapp/v1/metadata", nil)
	if e != nil {
		return e
	}
	r.Header.Set("User-Agent", "GuoApp-License/"+version)
	res, e := sendLicenseRequest(client, r)
	if e != nil {
		return licenseConnectionError(e)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("授权连接检测：服务返回 HTTP %d，未提交登录资料", res.StatusCode)
	}
	var m struct {
		Protocol, DistributionPublicKey, ServerPublicKey, AccountChallengePath, AccountLoginPath string
		UniversalAccounts                                                                        bool
	}
	raw, e := io.ReadAll(io.LimitReader(res.Body, 65537))
	if e != nil || len(raw) > 65536 || json.Unmarshal(raw, &m) != nil || m.Protocol != Protocol || !m.UniversalAccounts || m.ServerPublicKey != serverKey || m.DistributionPublicKey != distributionKey || m.AccountChallengePath != "/api/guoapp/v1/account-challenge" || m.AccountLoginPath != "/api/guoapp/v1/account-login" {
		return errors.New("授权连接检测：返回的服务身份或协议不匹配 [AUTH_SERVICE]")
	}
	return nil
}
