package hdx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/log"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	"github.com/jellydator/ttlcache/v3"

	"github.com/grafana/sqlds/v5"
	"github.com/grafana/sqlds/v5/hdx/models"
)

var (
	primaryKeyQueryString    = "SELECT primary_key FROM system.tables WHERE database='%s' AND table ='%s'"
	adHocKeyQuery            = "DESCRIBE %s"
	ErrPrimaryKeyNotFound    = backend.PluginError(errors.New("primary key not found"))
	ErrAdHocFilterKeysNotSet = backend.PluginError(errors.New("adHocFilter keys not found"))
)

// MetaDataProvider caches Hydrolix metadata (table primary key, ad-hoc
// filter keys) and exposes lookups used by HDX macros. It executes its
// queries through the upstream datasource's QueryData entry point — no
// Connector accessor is required because the plugin's
// DataSourceInstanceSettings are surfaced on the upstream service
// registry under instanceSettingsKey{}.
type MetaDataProvider struct {
	ds       *sqlds.SQLDatasource
	pkCache  *ttlcache.Cache[string, string]
	keyCache *ttlcache.Cache[string, map[string]string]
}

// NewMetaDataProvider constructs a provider with one-hour TTL caches,
// matching the fork's defaults.
func NewMetaDataProvider(ds *sqlds.SQLDatasource) *MetaDataProvider {
	return &MetaDataProvider{
		ds:       ds,
		pkCache:  ttlcache.New[string, string](ttlcache.WithTTL[string, string](time.Hour)),
		keyCache: ttlcache.New[string, map[string]string](ttlcache.WithTTL[string, map[string]string](time.Hour)),
	}
}

func (p *MetaDataProvider) GetPK(ctx context.Context, headers http.Header, database, table string) (string, error) {
	if database == "" {
		defaultDB, err := p.getDefaultDatabase(ctx)
		if err != nil {
			return "", err
		}
		database = defaultDB
	}
	cacheKey := fmt.Sprintf("%s_%s", database, table)
	if entry := p.pkCache.Get(cacheKey); entry != nil {
		log.DefaultLogger.Debug("Cache hit", "key", cacheKey)
		return entry.Value(), nil
	}
	log.DefaultLogger.Debug("Cache miss", "key", cacheKey)
	pk, err := p.QueryPK(ctx, headers, database, table)
	if err != nil {
		return "", err
	}
	p.pkCache.Set(cacheKey, pk, ttlcache.DefaultTTL)
	return pk, nil
}

func (p *MetaDataProvider) GetKeys(ctx context.Context, headers http.Header, cte string) (map[string]string, error) {
	if entry := p.keyCache.Get(cte); entry != nil {
		log.DefaultLogger.Debug("Cache hit", "key", cte)
		return entry.Value(), nil
	}
	log.DefaultLogger.Debug("Cache miss", "key", cte)
	keys, err := p.QueryKeys(ctx, headers, cte)
	if err != nil {
		return nil, err
	}
	p.keyCache.Set(cte, keys, ttlcache.DefaultTTL)
	return keys, nil
}

func (p *MetaDataProvider) getDefaultDatabase(ctx context.Context) (string, error) {
	v, ok := p.ds.Resolve(instanceSettingsKey{})
	if !ok {
		return "", errors.New("hdx: instance settings not registered — call hdx.NewDatasource(driver) and stash settings on first NewDatasource(ctx, settings)")
	}
	settings, ok := v.(backend.DataSourceInstanceSettings)
	if !ok {
		return "", fmt.Errorf("hdx: instanceSettingsKey held %T, want backend.DataSourceInstanceSettings", v)
	}
	parsed, err := models.NewPluginSettings(ctx, settings)
	if err != nil {
		return "", err
	}
	return parsed.DefaultDatabase, nil
}

func (p *MetaDataProvider) instanceSettings() (backend.DataSourceInstanceSettings, error) {
	v, ok := p.ds.Resolve(instanceSettingsKey{})
	if !ok {
		return backend.DataSourceInstanceSettings{}, errors.New("hdx: instance settings not registered")
	}
	settings, ok := v.(backend.DataSourceInstanceSettings)
	if !ok {
		return backend.DataSourceInstanceSettings{}, fmt.Errorf("hdx: instanceSettingsKey held %T", v)
	}
	return settings, nil
}

func (p *MetaDataProvider) executeQuery(ctx context.Context, headers http.Header, sql, queryID string) (*data.Frame, error) {
	queryJSON, err := json.Marshal(map[string]any{"rawSql": sql, "format": 1})
	if err != nil {
		return nil, err
	}
	newHeaders := make(map[string]string, len(headers))
	for k := range headers {
		newHeaders[k] = headers.Get(k)
	}
	dataQuery := backend.DataQuery{RefID: queryID, JSON: queryJSON}
	settings, err := p.instanceSettings()
	if err != nil {
		return nil, err
	}
	req := &backend.QueryDataRequest{
		PluginContext: backend.PluginContext{DataSourceInstanceSettings: &settings},
		Queries:       []backend.DataQuery{dataQuery},
		Headers:       newHeaders,
	}
	resp, err := p.ds.QueryData(ctx, req)
	if err != nil {
		return nil, err
	}
	dr, ok := resp.Responses[dataQuery.RefID]
	if !ok {
		return nil, fmt.Errorf("no response for query %s", queryID)
	}
	if dr.Error != nil {
		return nil, dr.Error
	}
	if len(dr.Frames) == 0 {
		return nil, fmt.Errorf("no frames in response")
	}
	return dr.Frames[0], nil
}

func (p *MetaDataProvider) QueryPK(ctx context.Context, headers http.Header, database, table string) (string, error) {
	formattedSQL := fmt.Sprintf(primaryKeyQueryString, database, table)
	frame, err := p.executeQuery(ctx, headers, formattedSQL, "pk_query")
	if err != nil {
		return "", err
	}
	if len(frame.Fields) == 0 {
		return "", ErrPrimaryKeyNotFound
	}
	field := frame.Fields[0]
	if field.Len() == 0 {
		return "", ErrPrimaryKeyNotFound
	}
	return p.GetStringSafe(field.At(0))
}

func (p *MetaDataProvider) QueryKeys(ctx context.Context, headers http.Header, cte string) (map[string]string, error) {
	if strings.Contains(strings.ToUpper(cte), "SELECT") {
		cte = fmt.Sprintf("(%s)", cte)
	}
	formattedSQL := fmt.Sprintf(adHocKeyQuery, cte)
	frame, err := p.executeQuery(ctx, headers, formattedSQL, "key_query")
	if err != nil {
		return nil, err
	}
	if len(frame.Fields) < 2 {
		return nil, ErrAdHocFilterKeysNotSet
	}
	keyField := frame.Fields[0]
	typeField := frame.Fields[1]
	keys := make(map[string]string, keyField.Len())
	for i := range keyField.Len() {
		key, err := p.GetStringSafe(keyField.At(i))
		if err != nil {
			return nil, err
		}
		keyType, err := p.GetStringSafe(typeField.At(i))
		if err != nil {
			return nil, err
		}
		keys[key] = keyType
	}
	return keys, nil
}

func (p *MetaDataProvider) GetStringSafe(v any) (string, error) {
	switch x := v.(type) {
	case string:
		return x, nil
	case *string:
		if x == nil {
			return "", nil
		}
		return *x, nil
	}
	return "", errors.New("invalid type")
}
