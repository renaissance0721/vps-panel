export type ProbeType = 'tcp' | 'icmp'
export type ProbeOutcome = 'success' | 'timeout' | 'dns_error' | 'connect_error' | 'permission_error' | 'cancelled'
export type ProbeTask = {
  id: number
  name: string
  type: ProbeType
  target: string
  port: number | null
  interval_seconds: number
  enabled: boolean
  server_ids: number[]
  created_at: string
  updated_at: string
}
export type ProbeInput = Omit<ProbeTask, 'id' | 'created_at' | 'updated_at'>
export type ProbeSummary = Pick<ProbeTask, 'id' | 'name' | 'type' | 'target' | 'port' | 'interval_seconds'> & {
  latest_latency_ms: number | null
  latest_outcome: ProbeOutcome | ''
  failure_rate: number | null
}
export type ProbeSample = {
  task_id: number
  timestamp: string
  latency_ms: number | null
  outcome: ProbeOutcome
}
export type LatencyHistory = { tasks: ProbeSummary[]; samples: ProbeSample[]; from: string; to: string }
export type LatencyHours = 1 | 6 | 24
