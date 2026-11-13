package infrastructure

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	spaceapp "github.com/yyhuni/lunafox/server/internal/modules/spacesearch/application"
)

const FofaBaseURL = "https://fofa.info"
const fofaFields = "host,ip,port,protocol,domain,title,server,country,country_name,region,city,icp,base_protocol"

type FofaClient struct {
	baseURL string
	client  *http.Client
}
type FofaError struct{ Message string }

func (e *FofaError) Error() string { return e.Message }
func (e *FofaError) Unwrap() error { return spaceapp.ErrProvider }

// Production wiring fixes the endpoint. A local fixture can replace it in
// tests. Redirects never receive the credential query.
func NewFofaClient(baseURL string, client *http.Client) *FofaClient {
	if baseURL == "" {
		baseURL = FofaBaseURL
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if copyClient.Timeout == 0 {
		copyClient.Timeout = 30 * time.Second
	}
	return &FofaClient{baseURL: strings.TrimRight(baseURL, "/"), client: &copyClient}
}

type fofaResponse struct {
	Error   *bool               `json:"error"`
	Message string              `json:"errmsg"`
	Size    *int64              `json:"size"`
	Results [][]json.RawMessage `json:"results"`
}

func (c *FofaClient) request(ctx context.Context, credentials spaceapp.Credentials, path string, parameters url.Values) (fofaResponse, error) {
	var output fofaResponse
	parameters.Set("email", credentials.Email)
	parameters.Set("key", credentials.APIKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path+"?"+parameters.Encode(), nil)
	if err != nil {
		return output, &FofaError{Message: "无法创建 FOFA 请求"}
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return output, &FofaError{Message: "FOFA 连接失败或请求超时"}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return output, &FofaError{Message: fmt.Sprintf("FOFA 返回 HTTP %d", resp.StatusCode)}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024+1))
	if err != nil || len(data) > 4*1024*1024 {
		return output, &FofaError{Message: "FOFA 返回内容过大或读取失败"}
	}
	if err = json.Unmarshal(data, &output); err != nil || output.Error == nil {
		return output, &FofaError{Message: "FOFA 返回的数据格式无效"}
	}
	if *output.Error {
		message := output.Message
		for _, secret := range []string{credentials.APIKey, url.QueryEscape(credentials.APIKey), credentials.Email, url.QueryEscape(credentials.Email)} {
			if secret != "" {
				message = strings.ReplaceAll(message, secret, "[已隐藏]")
			}
		}
		runes := []rune(strings.TrimSpace(message))
		if len(runes) > 300 {
			runes = runes[:300]
		}
		message = string(runes)
		if message == "" {
			message = "查询被拒绝，请检查账户权限、查询语法及额度"
		}
		return output, &FofaError{Message: "FOFA：" + message}
	}
	return output, nil
}
func (c *FofaClient) Test(ctx context.Context, credentials spaceapp.Credentials) error {
	_, err := c.request(ctx, credentials, "/api/v1/info/my", url.Values{})
	return err
}
func scalar(raw json.RawMessage) (string, error) {
	if string(raw) == "null" {
		return "", nil
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return value, nil
	}
	var number json.Number
	if json.Unmarshal(raw, &number) == nil {
		return number.String(), nil
	}
	return "", spaceapp.ErrProvider
}
func (c *FofaClient) Search(ctx context.Context, credentials spaceapp.Credentials, input spaceapp.SearchInput) (spaceapp.SearchResult, error) {
	parameters := url.Values{"qbase64": {base64.StdEncoding.EncodeToString([]byte(input.Query))}, "fields": {fofaFields}, "page": {strconv.Itoa(input.Page)}, "size": {strconv.Itoa(input.PageSize)}, "full": {strconv.FormatBool(input.Full)}}
	response, err := c.request(ctx, credentials, "/api/v1/search/all", parameters)
	if err != nil {
		return spaceapp.SearchResult{}, err
	}
	if response.Size == nil || *response.Size < 0 || response.Results == nil || len(response.Results) > input.PageSize {
		return spaceapp.SearchResult{}, &FofaError{Message: "FOFA 返回的结果集合无效"}
	}
	result := spaceapp.SearchResult{Total: *response.Size, Results: make([]spaceapp.Record, 0, len(response.Results))}
	for _, row := range response.Results {
		if len(row) != 13 {
			return spaceapp.SearchResult{}, &FofaError{Message: "FOFA 返回字段与请求字段不匹配"}
		}
		values := make([]string, len(row))
		for i, item := range row {
			values[i], err = scalar(item)
			if err != nil {
				return spaceapp.SearchResult{}, &FofaError{Message: "FOFA 结果字段格式无效"}
			}
		}
		port, _ := strconv.Atoi(values[2])
		result.Results = append(result.Results, spaceapp.Record{Host: values[0], IP: values[1], Port: port, Protocol: values[3], Domain: values[4], Title: values[5], Server: values[6], Country: values[7], CountryName: values[8], Region: values[9], City: values[10], ICP: values[11], BaseProtocol: values[12]})
	}
	return result, nil
}
