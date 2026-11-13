import { Suspense } from "react"
import { ApplicationOverview } from "@/components/assets/application-overview"

export default async function ApplicationPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params
  return <Suspense><ApplicationOverview id={id} /></Suspense>
}
