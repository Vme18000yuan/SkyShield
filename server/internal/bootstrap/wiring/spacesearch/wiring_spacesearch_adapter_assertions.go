package spacesearchwiring

import (
	spaceapp "github.com/yyhuni/lunafox/server/internal/modules/spacesearch/application"
	spacerepo "github.com/yyhuni/lunafox/server/internal/modules/spacesearch/repository"
)

var _ spaceapp.AssetImporter = (*spacesearchAssetImporterAdapter)(nil)
var _ spaceapp.Store = (*spacerepo.FofaStore)(nil)
