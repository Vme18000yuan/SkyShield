// ARTEX source, AGPL-3.0-only; see third_party/artex/SKYSHIELD-NOTICE.md.
import type { Activity } from "@/types/artex-ai.types";

// History and live responses can overlap or arrive out of order. A persisted
// activity's seq is its identity; replay must not duplicate rows or token totals.
export function mergeActivities(current: Activity[], incoming: Activity[]): Activity[] {
  if (!incoming.length) return current;
  const ordered = (items: Activity[]) => items.every((item, i) => !i || items[i - 1].seq < item.seq);
  const normalized = (items: Activity[]) => ordered(items) ? items : [...new Map(items.map(item => [item.seq, item])).values()].sort((a, b) => a.seq - b.seq);
  const left = normalized(current), right = normalized(incoming);
  if (!left.length) return right;
  if (right[0].seq > left[left.length - 1].seq) return [...left, ...right];
  const result: Activity[] = [];
  let i = 0, j = 0;
  while (i < left.length && j < right.length) {
    if (left[i].seq < right[j].seq) result.push(left[i++]);
    else { if (left[i].seq === right[j].seq) i++; result.push(right[j++]); }
  }
  return [...result, ...left.slice(i), ...right.slice(j)];
}
