package repository

import (
	"context"
	"fmt"

	"github.com/lib/pq"
	assetdomain "github.com/yyhuni/lunafox/server/internal/modules/asset/domain"
	"github.com/yyhuni/lunafox/server/internal/pkg/dbtx"
	"gorm.io/gorm"
)

func mergeWebObservationRow(ctx context.Context, db *gorm.DB, table string, targetID int, rawURL string, observation assetdomain.SparseWebObservation) error {
	query := fmt.Sprintf(`UPDATE %s SET
		title=CASE WHEN ?<>'' THEN ? ELSE title END,
		webserver=CASE WHEN ?<>'' THEN ? ELSE webserver END,
		status_code=COALESCE(?,status_code),content_length=COALESCE(?,content_length),
		tech=ARRAY(SELECT DISTINCT item FROM unnest(tech || COALESCE(?::varchar[],'{}')) AS item ORDER BY item)
		WHERE target_id=? AND url=?`, table)
	return dbtx.Resolve(ctx, db).WithContext(ctx).Exec(query, observation.Title, observation.Title, observation.Webserver, observation.Webserver, observation.StatusCode, observation.ContentLength, pq.StringArray(observation.Technologies), targetID, rawURL).Error
}

func (r *WebsiteRepository) MergeWebObservationContext(ctx context.Context, targetID int, rawURL string, observation assetdomain.SparseWebObservation) error {
	return mergeWebObservationRow(ctx, r.db, "website", targetID, rawURL, observation)
}

func (r *EndpointRepository) MergeWebObservationContext(ctx context.Context, targetID int, rawURL string, observation assetdomain.SparseWebObservation) error {
	return mergeWebObservationRow(ctx, r.db, "endpoint", targetID, rawURL, observation)
}
