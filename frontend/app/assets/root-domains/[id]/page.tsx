import { Suspense } from "react"
import { RootDomainOverview } from "@/components/assets/root-domain-overview"

export default async function RootDomainPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params
  return <Suspense><RootDomainOverview id={id} /></Suspense>
}
