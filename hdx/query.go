package hdx

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"

	"github.com/grafana/sqlds/v5/hdx/models"
)

// HDXQuery is the Hydrolix-flavoured DataQuery model. It carries the raw
// SQL, the per-query settings, the ad-hoc filter list, and the request-
// scoped TimeRange/Interval/Headers.
type HDXQuery struct {
	RawSQL        string                `json:"rawSql"`
	Format        int                   `json:"format"`
	Round         string                `json:"round,omitempty"`
	QuerySettings []models.QuerySetting `json:"querySettings,omitempty"`
	Filters       []AdHocFilter         `json:"filters,omitempty"`
	Meta          struct {
		TimeZone string `json:"timezone"`
	} `json:"meta"`
	TimeRange backend.TimeRange `json:"-"`
	Interval  time.Duration     `json:"-"`
	Headers   http.Header       `json:"-"`
}

// AdHocFilter is one entry from Grafana's ad-hoc filter UI.
type AdHocFilter struct {
	Key      string   `json:"key"`
	Operator string   `json:"operator"`
	Value    string   `json:"value"`
	Values   []string `json:"values,omitempty"`
}

// WithSQL returns a shallow copy of q with RawSQL replaced. Macros that
// need to inspect the in-flight SQL during expansion call WithSQL to avoid
// mutating the cached HDXQuery on the registry.
func (q *HDXQuery) WithSQL(rawSql string) *HDXQuery {
	return &HDXQuery{
		RawSQL:        rawSql,
		Format:        q.Format,
		Round:         q.Round,
		QuerySettings: q.QuerySettings,
		Filters:       q.Filters,
		Meta:          q.Meta,
		TimeRange:     q.TimeRange,
		Interval:      q.Interval,
		Headers:       q.Headers,
	}
}

// GetHdxQuery parses a backend.DataQuery into an HDXQuery, copying the
// time range, interval and headers onto the result. Useful for callers
// outside the interpolation hook path (e.g. plugin's own resource routes).
func GetHdxQuery(query backend.DataQuery, headers http.Header, timeRange *backend.TimeRange, interval *time.Duration) (*HDXQuery, error) {
	q := &HDXQuery{}
	if err := json.Unmarshal(query.JSON, &q); err != nil {
		return nil, backend.DownstreamError(fmt.Errorf("error unmarshaling query JSON to the Query Model: %v", err))
	}
	if timeRange == nil {
		timeRange = &query.TimeRange
	}
	if interval == nil {
		interval = &query.Interval
	}
	return &HDXQuery{
		RawSQL:        q.RawSQL,
		Format:        q.Format,
		Round:         q.Round,
		QuerySettings: q.QuerySettings,
		Filters:       q.Filters,
		Meta:          q.Meta,
		TimeRange:     *timeRange,
		Interval:      *interval,
		Headers:       headers,
	}, nil
}
