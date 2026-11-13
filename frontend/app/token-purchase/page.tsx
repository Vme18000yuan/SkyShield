import { redirect } from "next/navigation"
import { TOKEN_PURCHASE_URL } from "@/lib/external-links"

export default function TokenPurchasePage() {
  redirect(TOKEN_PURCHASE_URL)
}
