import { createCoverageForceEngine } from "./coverage-force-engine"
import type { ForceFrame, ForceRequest } from "./coverage-force-layout"

const scope = self as unknown as {
  onmessage: ((event: MessageEvent<ForceRequest>) => void) | null
  postMessage: (frame: ForceFrame) => void
}
const engine = createCoverageForceEngine(frame => scope.postMessage(frame))
scope.onmessage = event => engine.receive(event.data)
