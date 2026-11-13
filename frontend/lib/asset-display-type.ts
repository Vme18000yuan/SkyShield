import type { AssetDisplayType, AssetEntity } from "@/types/asset-entity.types"

// Older servers may not send displayType. Recognize HTTP(S) consistently
// while retaining the canonical service/endpoint identity for actions.
export function assetDisplayType(asset: Pick<AssetEntity, "assetType" | "label" | "displayType">): AssetDisplayType {
  if (asset.displayType) return asset.displayType
  if ((asset.assetType === "service" || asset.assetType === "endpoint") && /^https?:\/\//i.test(asset.label)) return "web"
  return asset.assetType
}
