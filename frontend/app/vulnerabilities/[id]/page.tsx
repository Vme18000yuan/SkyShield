import { notFound } from "next/navigation"
import { RegisteredVulnerability } from "@/components/vulnerabilities/registered-vulnerability"

export default async function VulnerabilityPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params
  if (!/^[1-9]\d*$/.test(id) || Number(id) > 2147483647) notFound()
  return <RegisteredVulnerability id={Number(id)} />
}
