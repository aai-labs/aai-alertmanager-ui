import DataState from "./DataState";
import { useEffect, useRef, useState } from "react";
import { api } from "./api";
import type { Alert, Context } from "./model";
import { utcDate } from "./incidents";
import { heatLevel, dayDescription, type AnalyticsResponse } from "./analytics";
import {
  aggregateObservedTrend,
  observedTotal,
  type ObservedResourcePoint,
  bytesText,
  controllerNeedsAttention,
  known,
  needsAttention,
  numberText,
  podStates,
  podAttentionOrder,
  ratioText,
  quotaValue,
  sumKnown,
  workspaceLink,
  type Controller,
  type NamespaceResources,
  type NamespaceWorkloads,
  type Pod,
  type PodState,
  type ResourcePoint,
  type ResourcesResponse,
  type WorkloadsResponse,
} from "./cluster";
type Props = {
  mode: "overview" | "resources" | "workloads";
  context: Context;
  namespace: string;
  setNamespace: (s: string) => void;
  query: string;
  setQuery: (s: string) => void;
  alerts: Alert[];
  alertsStale: boolean;
};
function useSource<T>(path: string | undefined, revision: number) {
  const [data, setData] = useState<T>(),
    [error, setError] = useState(""),
    [busy, setBusy] = useState(!!path);
  useEffect(() => {
    let live = true;
    setData(undefined);
    setError("");
    if (!path) {
      setBusy(false);
      return;
    }
    setBusy(true);
    api<T>(path, undefined, 25000)
      .then((d) => {
        if (live) setData(d);
      })
      .catch((e) => {
        if (live) setError(e.message);
      })
      .finally(() => {
        if (live) setBusy(false);
      });
    return () => {
      live = false;
    };
  }, [path, revision]);
  return { data, error, busy };
}
export default function ClusterWorkspace({
  mode,
  context,
  namespace,
  setNamespace,
  query,
  setQuery,
  alerts,
  alertsStale,
}: Props) {
  const [hours, setHours] = useState("24"),
    [metric, setMetric] = useState<"cpu" | "memory">("cpu"),
    [revision, setRevision] = useState(0),
    [pod, setPod] = useState<{ pod: Pod; namespace: string }>();
  const scope = namespace === "__cluster__" ? "" : namespace;
  const params = new URLSearchParams({ hours });
  if (scope) params.set("namespace", scope);
  const nsParams = new URLSearchParams();
  if (scope) nsParams.set("namespace", scope);
  const resource = useSource<ResourcesResponse>(
    context.resourcesEnabled && mode !== "workloads"
      ? `resources?${params}`
      : undefined,
    revision,
  );
  const workloads = useSource<WorkloadsResponse>(
    context.workloadsEnabled && mode !== "resources"
      ? `workloads?${nsParams}`
      : undefined,
    revision,
  );
  const analytics = useSource<AnalyticsResponse>(
    context.analyticsEnabled && mode === "overview"
      ? `analytics?days=7&severity=actionable${scope ? "&namespace=" + encodeURIComponent(scope) : ""}`
      : undefined,
    revision,
  );
  useEffect(() => {
    const timer = setInterval(() => setRevision((v) => v + 1), 60000);
    return () => clearInterval(timer);
  }, []);
  const [namespaces, setNamespaces] = useState<string[]>(
    context.namespaces || [],
  );
  useEffect(() => {
    setNamespaces((old) =>
      [
        ...new Set(
          [
            ...old,
            ...(resource.data?.namespaces || []).map((r) => r.namespace),
            ...(workloads.data?.namespaces || []).map((r) => r.namespace),
          ].filter(Boolean),
        ),
      ].sort(),
    );
  }, [resource.data, workloads.data]);
  const rows = resource.data?.namespaces || [],
    workRows = workloads.data?.namespaces || [];
  const allPods = workRows
    .flatMap((n) => n.pods.map((p) => ({ pod: p, namespace: n.namespace })))
    .sort((a, b) => podAttentionOrder(a.pod, b.pod));
  const attention = allPods.filter((p) => needsAttention(p.pod));
  const busy = resource.busy || workloads.busy;
  const title =
    mode === "overview"
      ? "What’s under pressure?"
      : mode === "resources"
        ? "Resource demand, in context."
        : "What’s running, and what needs attention.";
  return (
    <section className="enhanced-workspace cluster-workspace">
      <div className="section-head">
        <div>
          <h2>{title}</h2>
          <p className="muted">
            {scope ||
              (context.tenantMode
                ? "All permitted namespaces"
                : "Cluster overview")}{" "}
            · Prometheus observations
          </p>
        </div>
        <button disabled={busy} onClick={() => setRevision((v) => v + 1)}>
          {busy ? "Refreshing…" : "Refresh context"}
        </button>
      </div>
      <div className="filters">
        <label>
          Namespace{" "}
          <select
            aria-label="Context namespace"
            value={scope}
            onChange={(e) => setNamespace(e.target.value)}
          >
            <option value="">
              {context.tenantMode
                ? "All permitted namespaces"
                : "All namespaces"}
            </option>
            {scope && !namespaces.includes(scope) && <option>{scope}</option>}
            {namespaces.map((n) => (
              <option key={n}>{n}</option>
            ))}
          </select>
        </label>
        {mode !== "workloads" && (
          <label>
            History{" "}
            <select
              aria-label="Resource history"
              value={hours}
              onChange={(e) => setHours(e.target.value)}
            >
              <option value="24">Last 24 hours</option>
              <option value="168">Last 7 days</option>
            </select>
          </label>
        )}
      </div>
      {resource.error && (
        <div className="banner danger" role="alert">
          Resource metrics unavailable: {resource.error}
        </div>
      )}
      {workloads.error && (
        <div className="banner danger" role="alert">
          Workload inventory unavailable: {workloads.error}
        </div>
      )}
      {resource.data?.incomplete && (
        <CollectionWarnings
          title="Partial resource metrics"
          warnings={resource.data.warnings}
        />
      )}
      {workloads.data?.incomplete && (
        <CollectionWarnings
          title="Partial workload inventory"
          warnings={workloads.data.warnings}
        />
      )}
      {mode !== "resources" &&
        workloads.data &&
        workloads.data.source.state !== "unavailable" &&
        allPods.length > 0 && (
          <div
            className={`workload-attention ${attention.length ? "has-issues" : ""}`}
          >
            <div>
              <strong>{attention.length} observed pods need attention</strong>
              <span className="muted">
                {" "}
                · {allPods.filter((p) => p.pod.state === "notReady").length} not
                ready ·{" "}
                {allPods.filter((p) => p.pod.state === "unscheduled").length}{" "}
                not scheduled ·{" "}
                {allPods.filter((p) => p.pod.state === "failed").length} failed
                · {allPods.filter((p) => p.pod.state === "unknown").length}{" "}
                unknown
              </span>
            </div>
            {mode === "overview" && (
              <a
                href={
                  workspaceLink("workloads", scope) + "&workloadState=attention"
                }
              >
                Find them →
              </a>
            )}
          </div>
        )}
      {mode !== "workloads" && (
        <>
          {resource.busy ? (
            <DataState loading label="Loading resource usage…" />
          ) : (
            <ResourceCards
              rows={rows}
              loading={resource.busy}
              cluster={resource.data?.cluster || null}
              showCapacity={!scope && !context.tenantMode}
            />
          )}
          <div className={mode === "overview" ? "cluster-columns" : ""}>
            <section className="analytics-panel resource-trend">
              <div className="section-head">
                <div>
                  <h3>Workload resource trend</h3>
                  <p className="muted">Observed namespace subtotals · UTC</p>
                </div>
                <div className="segments">
                  <button
                    aria-pressed={metric === "cpu"}
                    onClick={() => setMetric("cpu")}
                  >
                    CPU
                  </button>
                  <button
                    aria-pressed={metric === "memory"}
                    onClick={() => setMetric("memory")}
                  >
                    Memory
                  </button>
                </div>
              </div>
              {resource.busy ? (
                <DataState loading label="Loading resource history…" />
              ) : resource.error ? (
                <DataState
                  label="Resource history unavailable"
                  detail="Refresh context to try again."
                />
              ) : (
                <ResourceTrend
                  points={aggregateObservedTrend(rows)}
                  metric={metric}
                />
              )}
              <div className="chart-legend">
                <span>
                  <i className="legend-swatch usage" />
                  Usage
                </span>
                <span>
                  <i className="legend-swatch requests" />
                  Requests
                </span>
              </div>
              <p className="muted enhanced-footnote">
                Requests reserve resources; they are not a usage ceiling. Values
                are observed subtotals, not complete-scope totals. Reporting
                namespaces may vary; inspect the data table. Workload usage
                excludes host processes.
              </p>
            </section>
            {mode === "overview" && (
              <section className="analytics-panel">
                <div className="section-head">
                  <div>
                    <h3>Pods, at a glance</h3>
                    <p className="muted">Observed readiness and scheduling</p>
                  </div>
                  <span className="muted">{allPods.length} pods</span>
                </div>
                {workloads.busy ? (
                  <DataState loading label="Loading workload inventory…" />
                ) : workloads.error ? (
                  <DataState label="Pod inventory unavailable" />
                ) : (
                  <>
                    <div className="pod-grid">
                      {allPods.slice(0, 500).map((p) => (
                        <button
                          key={p.namespace + "/" + (p.pod.uid || p.pod.name)}
                          className={`pod-tile ${p.pod.state}`}
                          title={`${p.namespace} / ${p.pod.name}: ${podStates[p.pod.state]}`}
                          aria-label={`${p.namespace}, ${p.pod.name}, ${podStates[p.pod.state]}`}
                          onClick={() => setPod(p)}
                        />
                      ))}
                    </div>
                    {allPods.length > 500 && (
                      <p className="muted">
                        First 500 pods shown, attention first and completed
                        last. Open Workloads for the full received inventory.
                      </p>
                    )}
                    {!allPods.length && (
                      <p className="muted">
                        No pod inventory available for this scope.
                      </p>
                    )}
                    <div className="pod-legend">
                      {(Object.keys(podStates) as PodState[]).map((state) => (
                        <span key={state}>
                          <i className={`pod-tile ${state}`} />
                          {
                            allPods.filter((p) => p.pod.state === state).length
                          }{" "}
                          {podStates[state].toLowerCase()}
                        </span>
                      ))}
                    </div>
                  </>
                )}
                <p className="muted enhanced-footnote">
                  Running does not mean ready. Readiness does not prove
                  end-to-end service availability.
                </p>
              </section>
            )}
          </div>
        </>
      )}
      {mode === "overview" && (
        <section className="analytics-panel">
          <div className="section-head">
            <div>
              <h3>Where to look first</h3>
              <p className="muted">
                Resources, current readiness, and recurring alerts.
              </p>
            </div>
            <span className="muted">Alert history · 7 UTC days</span>
          </div>
          {analytics.busy && (
            <DataState loading label="Loading namespace alert history…" />
          )}
          {(resource.busy || workloads.busy) && (
            <DataState loading label="Loading namespace comparison…" />
          )}
          {!resource.busy &&
            !workloads.busy &&
            !resource.error &&
            !workloads.error &&
            !rows.length &&
            !workRows.length && (
              <DataState
                label="No namespace observations"
                detail="No resource or workload metrics were returned for this scope."
              />
            )}
          {analytics.error && (
            <p role="status" className="muted">
              Alert history unavailable: {analytics.error}
            </p>
          )}
          {analytics.data?.incomplete && (
            <p className="muted">
              Alert history is incomplete; striped cells have unknown coverage.
            </p>
          )}
          <div className="analytics-scroll">
            <table className="analytics-table resource-table">
              <thead>
                <tr>
                  <th>Namespace</th>
                  <th>CPU usage / requests</th>
                  <th>Memory usage / requests</th>
                  <th>Ready / active pods</th>
                  <th>Firing time by day</th>
                  <th>Alert context</th>
                </tr>
              </thead>
              <tbody>
                {[
                  ...new Set([
                    ...rows.map((r) => r.namespace),
                    ...workRows.map((r) => r.namespace),
                    ...(analytics.data?.namespaces || []).map(
                      (n) => n.namespace,
                    ),
                  ]),
                ]
                  .sort()
                  .map((ns) => {
                    const r = rows.find((r) => r.namespace === ns),
                      w = workRows.find((w) => w.namespace === ns),
                      history = analytics.data?.namespaces.find(
                        (n) => n.namespace === ns,
                      );
                    const active = w?.pods.filter(
                      (p) => p.state !== "completed" && p.state !== "failed",
                    );
                    return (
                      <tr key={ns}>
                        <td>
                          <button
                            className="text-button"
                            onClick={() => setNamespace(ns)}
                          >
                            {ns}
                          </button>
                        </td>
                        <td>
                          {resource.busy
                            ? "Loading…"
                            : numberText(r?.cpuUsageCores)}{" "}
                          / {numberText(r?.cpuRequestsCores)}
                          <small>cores</small>
                        </td>
                        <td>
                          {resource.busy
                            ? "Loading…"
                            : bytesText(r?.memoryUsageBytes)}{" "}
                          / {bytesText(r?.memoryRequestsBytes)}
                        </td>
                        <td>
                          {workloads.busy
                            ? "Loading…"
                            : w
                              ? `${active?.filter((p) => p.state === "ready").length} / ${active?.length}`
                              : "Unknown"}
                          {w && (
                            <small>
                              {w.pods.filter(needsAttention).length} need
                              attention
                            </small>
                          )}
                        </td>
                        <td>
                          {history ? (
                            <div className="mini-heatmap">
                              {history.days.map((d) => (
                                <span
                                  role="img"
                                  key={d.date}
                                  className={`heat-cell ${heatLevel(d)} ${d.knownSeconds < d.expectedSeconds ? "partial" : ""}`}
                                  title={dayDescription(d)}
                                  aria-label={dayDescription(d)}
                                />
                              ))}
                            </div>
                          ) : (
                            <span className="muted">
                              {analytics.busy
                                ? "Loading…"
                                : analytics.error
                                  ? "Unavailable"
                                  : "No observed history"}
                            </span>
                          )}
                        </td>
                        <td>
                          <a href={workspaceLink("all", ns)}>
                            {alertsStale
                              ? "View alerts"
                              : `${alerts.filter((a) => a.labels.namespace === ns).length} alerts`}{" "}
                            →
                          </a>
                          {context.incidentsEnabled && (
                            <small>
                              <a href={workspaceLink("incidents", ns)}>
                                Incidents →
                              </a>
                            </small>
                          )}
                        </td>
                      </tr>
                    );
                  })}
              </tbody>
            </table>
          </div>
          <p className="muted enhanced-footnote">
            Alert counts are instances, not incidents. Zero alerts does not
            establish health.{" "}
            <a href={workspaceLink("analytics", scope)}>
              Open alert analytics →
            </a>
          </p>
        </section>
      )}
      {mode === "resources" && resource.data && (
        <>
          {!rows.length && (
            <DataState
              label="No resource observations"
              detail="No metrics were returned for this namespace and time window."
            />
          )}
          <NamespaceResourceTable rows={rows} />
          <NamespaceQuotas rows={rows} />
          <section className="analytics-panel">
            <div className="section-head">
              <h3>Persistent volume claims</h3>
              <span className="muted">
                Namespace totals · not Ceph physical capacity
              </span>
            </div>
            {!rows.some(
              (r) =>
                known(r.pvcRequestedBytes) ||
                known(r.pvcCapacityBytes) ||
                known(r.pvcUsedBytes),
            ) && (
              <DataState
                label="No volume metrics observed"
                detail="This does not establish that no volumes exist."
              />
            )}
            <div className="analytics-scroll">
              <table className="analytics-table">
                <thead>
                  <tr>
                    <th>Namespace</th>
                    <th>Requested</th>
                    <th>Capacity</th>
                    <th>Used</th>
                    <th>Used / capacity</th>
                    <th>Context</th>
                  </tr>
                </thead>
                <tbody>
                  {rows.map((r) => (
                    <tr key={r.namespace}>
                      <td>{r.namespace}</td>
                      <td>{bytesText(r.pvcRequestedBytes)}</td>
                      <td>{bytesText(r.pvcCapacityBytes)}</td>
                      <td>{bytesText(r.pvcUsedBytes)}</td>
                      <td>{ratioText(r.pvcUsedBytes, r.pvcCapacityBytes)}</td>
                      <td>
                        <a href={workspaceLink("all", r.namespace)}>Alerts →</a>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </section>
          {!context.tenantMode && resource.data.nodes && (
            <section className="analytics-panel">
              <h3>Node utilization</h3>
              {resource.data.cluster && (
                <p>
                  Host totals: {numberText(resource.data.cluster.cpuUsageCores)}{" "}
                  / {numberText(resource.data.cluster.cpuAllocatableCores)}{" "}
                  allocatable CPU cores ·{" "}
                  {bytesText(resource.data.cluster.memoryUsageBytes)} /{" "}
                  {bytesText(resource.data.cluster.memoryAllocatableBytes)}{" "}
                  allocatable memory
                </p>
              )}
              <p className="muted">
                All observed nodes · CPU is non-idle cores; memory is total
                minus available. Includes host processes, unlike workload
                working-set memory.
              </p>
              <div className="analytics-scroll">
                <table className="analytics-table">
                  <thead>
                    <tr>
                      <th>Node</th>
                      <th>CPU usage / allocatable</th>
                      <th>CPU proportion</th>
                      <th>Memory usage / allocatable</th>
                      <th>Memory proportion</th>
                    </tr>
                  </thead>
                  <tbody>
                    {resource.data.nodes.map((n) => (
                      <tr key={n.node}>
                        <td>{n.node}</td>
                        <td>
                          {numberText(n.cpuUsageCores)} /{" "}
                          {numberText(n.cpuAllocatableCores)} cores
                        </td>
                        <td>
                          {ratioText(n.cpuUsageCores, n.cpuAllocatableCores)}
                        </td>
                        <td>
                          {bytesText(n.memoryUsageBytes)} /{" "}
                          {bytesText(n.memoryAllocatableBytes)}
                        </td>
                        <td>
                          {ratioText(
                            n.memoryUsageBytes,
                            n.memoryAllocatableBytes,
                          )}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
              {!resource.data.nodes.length && (
                <p className="muted">No node metrics available.</p>
              )}
            </section>
          )}
        </>
      )}
      {mode === "workloads" && !workloads.error && (
        <WorkloadTables
          rows={workRows}
          busy={workloads.busy}
          query={query}
          setQuery={setQuery}
          onPod={setPod}
          context={context}
        />
      )}
      <p className="muted enhanced-footnote">
        {mode === "workloads"
          ? `Inventory observed ${utcDate(workloads.data?.generatedAt)}. Metrics older than ${workloads.data?.source.maxAgeSeconds || 180}s are not treated as current.`
          : `Resources observed ${utcDate(resource.data?.generatedAt)}. History resolution: ${resource.data?.resolutionSeconds || "—"} seconds.`}
      </p>
      {pod && (
        <PodDetail
          selected={pod}
          incidentsEnabled={!!context.incidentsEnabled}
          onClose={() => setPod(undefined)}
        />
      )}
    </section>
  );
}
function ResourceCards({
  rows,
  loading,
  cluster,
  showCapacity,
}: {
  rows: NamespaceResources[];
  loading: boolean;
  cluster: ResourcesResponse["cluster"];
  showCapacity: boolean;
}) {
  const cpu = observedTotal(rows.map((r) => r.cpuUsageCores)),
    memory = observedTotal(rows.map((r) => r.memoryUsageBytes)),
    cpuRequests = observedTotal(rows.map((r) => r.cpuRequestsCores)),
    memoryRequests = observedTotal(rows.map((r) => r.memoryRequestsBytes));
  return (
    <div className="overview resource-overview">
      <div className="stat">
        <span>Workload CPU usage</span>
        <strong>
          {loading ? "—" : numberText(cpu.value)}
          <small>cores</small>
        </strong>
        <p>
          Observed usage · {cpu.reporting}/{cpu.total} namespaces reporting
        </p>
        <p>
          {numberText(cpuRequests.value)} requested · {cpuRequests.reporting}/
          {cpuRequests.total} reporting
        </p>
        {showCapacity && (
          <p>
            {numberText(cluster?.cpuAllocatableCores)} allocatable · cluster
          </p>
        )}
      </div>
      <div className="stat">
        <span>Workload memory usage</span>
        <strong>{loading ? "—" : bytesText(memory.value)}</strong>
        <p>
          Observed usage · {memory.reporting}/{memory.total} namespaces
          reporting
        </p>
        <p>
          {bytesText(memoryRequests.value)} requested ·{" "}
          {memoryRequests.reporting}/{memoryRequests.total} reporting
        </p>
        {showCapacity && (
          <p>
            {bytesText(cluster?.memoryAllocatableBytes)} allocatable · cluster
          </p>
        )}
      </div>
      <div className="stat">
        <span>CPU limits</span>
        <strong>
          {loading
            ? "—"
            : numberText(sumKnown(rows.map((r) => r.cpuLimitsCores)))}
          <small>cores</small>
        </strong>
        <p>
          {rows.some((r) => (r.unlimitedCPUContainers ?? 0) > 0)
            ? `${rows.reduce((n, r) => n + (r.unlimitedCPUContainers ?? 0), 0)} containers without an observed limit`
            : "Configured limits · not reservations"}
        </p>
      </div>
      <div className="stat">
        <span>Memory limits</span>
        <strong>
          {loading
            ? "—"
            : bytesText(sumKnown(rows.map((r) => r.memoryLimitsBytes)))}
        </strong>
        <p>
          {rows.some((r) => (r.unlimitedMemoryContainers ?? 0) > 0)
            ? `${rows.reduce((n, r) => n + (r.unlimitedMemoryContainers ?? 0), 0)} containers without an observed limit`
            : "Configured limits · not reservations"}
        </p>
      </div>
    </div>
  );
}
function ResourceTrend({
  points,
  metric,
}: {
  points: ObservedResourcePoint[];
  metric: "cpu" | "memory";
}) {
  const usage = points.map((p) =>
      metric === "cpu" ? p.cpuUsageCores : p.memoryUsageBytes,
    ),
    requests = points.map((p) =>
      metric === "cpu" ? p.cpuRequestsCores : p.memoryRequestsBytes,
    ),
    max = Math.max(1, ...usage.filter(known), ...requests.filter(known)),
    left = 55,
    width = 900,
    height = 180;
  function paths(values: (number | null)[]) {
    const pieces: string[] = [];
    let path = "";
    for (let i = 0; i < values.length; i++) {
      const v = values[i];
      if (!known(v)) {
        if (path) pieces.push(path);
        path = "";
        continue;
      }
      const x = left + (i / Math.max(1, values.length - 1)) * width,
        y = 210 - (v / max) * height;
      path += `${path ? "L" : "M"}${x.toFixed(1)},${y.toFixed(1)} `;
    }
    if (path) pieces.push(path);
    return pieces;
  }
  return (
    <>
      {!points.length ? (
        <div className="empty">No historical resource samples available.</div>
      ) : (
        <>
          <svg
            viewBox="0 0 1000 260"
            role="img"
            className="resource-chart"
            aria-label={`${metric === "cpu" ? "CPU cores" : "Memory bytes"} usage and requests over time. Missing samples leave gaps. Exact values in the data table.`}
          >
            {[0, 0.5, 1].map((f) => (
              <g key={f}>
                <line
                  x1={left}
                  x2={left + width}
                  y1={210 - f * height}
                  y2={210 - f * height}
                  className="chart-grid"
                />
                <text x="0" y={214 - f * height} className="chart-label">
                  {metric === "cpu"
                    ? numberText(max * f, 1)
                    : bytesText(max * f)}
                </text>
              </g>
            ))}
            {paths(requests).map((d, i) => (
              <path
                key={"r" + i}
                d={d}
                stroke="#9292a8"
                strokeDasharray="5 4"
                fill="none"
                strokeWidth="2"
              />
            ))}
            {paths(usage).map((d, i) => (
              <path
                key={"u" + i}
                d={d}
                stroke="#0000f0"
                fill="none"
                strokeWidth="2.5"
              />
            ))}
            {usage.map((v, i) =>
              known(v) ? (
                <circle
                  key={"up" + i}
                  cx={left + (i / Math.max(1, usage.length - 1)) * width}
                  cy={210 - (v / max) * height}
                  r={usage.length < 30 ? 2.5 : 1}
                  fill="#0000f0"
                />
              ) : null,
            )}
            <text x={left} y="245" className="chart-label">
              {utcDate(points[0]?.at)}
            </text>
            <text x="960" y="245" textAnchor="end" className="chart-label">
              {utcDate(points.at(-1)?.at)}
            </text>
          </svg>
          <details>
            <summary>Resource trend data</summary>
            <div className="analytics-scroll trend-data">
              <table className="analytics-table">
                <thead>
                  <tr>
                    <th>Time (UTC)</th>
                    <th>Usage</th>
                    <th>Requests</th>
                    <th>Usage reporting</th>
                    <th>Requests reporting</th>
                  </tr>
                </thead>
                <tbody>
                  {points.map((p, i) => (
                    <tr key={p.at}>
                      <td>{utcDate(p.at)}</td>
                      <td>
                        {metric === "cpu"
                          ? numberText(usage[i])
                          : bytesText(usage[i])}
                      </td>
                      <td>
                        {metric === "cpu"
                          ? numberText(requests[i])
                          : bytesText(requests[i])}
                      </td>
                      <td>
                        {metric === "cpu"
                          ? p.usageReportingCPU
                          : p.usageReportingMemory}{" "}
                        / {p.namespaceCount} namespaces
                      </td>
                      <td>
                        {metric === "cpu"
                          ? p.requestsReportingCPU
                          : p.requestsReportingMemory}{" "}
                        / {p.namespaceCount} namespaces
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </details>
        </>
      )}
    </>
  );
}
function NamespaceResourceTable({ rows }: { rows: NamespaceResources[] }) {
  return (
    <section className="analytics-panel">
      <h3>Usage, requests, and limits</h3>
      {!rows.length && <DataState label="No namespace resource metrics" />}
      <p className="muted">
        Regular-container totals. Missing metrics and partially unlimited
        workloads are not reported as zero.
      </p>
      <div className="analytics-scroll">
        <table className="analytics-table resource-table">
          <thead>
            <tr>
              <th>Namespace</th>
              <th>CPU usage / requests / limits</th>
              <th>Memory usage / requests / limits</th>
              <th>Context</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => (
              <tr key={r.namespace}>
                <td>{r.namespace}</td>
                <td>
                  {numberText(r.cpuUsageCores)} /{" "}
                  {numberText(r.cpuRequestsCores)} /{" "}
                  {numberText(r.cpuLimitsCores)} cores
                  {(r.unlimitedCPUContainers ?? 0) > 0 && (
                    <small>
                      {r.unlimitedCPUContainers} containers without observed CPU
                      limits
                    </small>
                  )}
                </td>
                <td>
                  {bytesText(r.memoryUsageBytes)} /{" "}
                  {bytesText(r.memoryRequestsBytes)} /{" "}
                  {bytesText(r.memoryLimitsBytes)}
                  {(r.unlimitedMemoryContainers ?? 0) > 0 && (
                    <small>
                      {r.unlimitedMemoryContainers} containers without observed
                      memory limits
                    </small>
                  )}
                </td>
                <td>
                  <a href={workspaceLink("workloads", r.namespace)}>
                    Workloads →
                  </a>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <p className="muted enhanced-footnote">
        Requests are reservations; limits cap individual containers. Effective
        scheduler requests can also include init containers and pod overhead.
      </p>
    </section>
  );
}
function WorkloadTables({
  rows,
  busy,
  query,
  setQuery,
  onPod,
  context,
}: {
  rows: NamespaceWorkloads[];
  busy: boolean;
  query: string;
  setQuery: (q: string) => void;
  onPod: (p: { pod: Pod; namespace: string }) => void;
  context: Context;
}) {
  const [tab, setTab] = useState("pods"),
    [filter, setFilter] = useState(
      new URLSearchParams(location.search).get("workloadState") === "attention"
        ? "attention"
        : "all",
    );
  const [visibleLimit, setVisibleLimit] = useState(100);
  useEffect(() => setVisibleLimit(100), [tab, filter, query]);
  const search = query.toLowerCase();
  const pods = rows
    .flatMap((n) => n.pods.map((p) => ({ ...p, namespace: n.namespace })))
    .filter(
      (p) =>
        `${p.name} ${p.namespace} ${p.ownerName || ""} ${p.waitingReasons.join(" ")}`
          .toLowerCase()
          .includes(search) &&
        (filter === "all" ||
          (filter === "attention" ? needsAttention(p) : p.state === filter)),
    );
  pods.sort(podAttentionOrder);
  const controllers = rows
    .flatMap((n) =>
      n.controllers.map((c) => ({ ...c, namespace: n.namespace })),
    )
    .filter(
      (c) =>
        `${c.name} ${c.namespace} ${c.kind}`.toLowerCase().includes(search) &&
        (filter !== "attention" || controllerNeedsAttention(c)),
    );
  const services = rows
    .flatMap((n) => n.services.map((s) => ({ ...s, namespace: n.namespace })))
    .filter(
      (s) =>
        `${s.name} ${s.namespace}`.toLowerCase().includes(search) &&
        (filter !== "attention" ||
          s.reachability !== "reachable" ||
          s.endpointsReady === 0),
    );
  return (
    <section className="analytics-panel">
      <div className="section-head">
        <div>
          <h3>Workload inventory</h3>
          <p className="muted">
            Kubernetes metrics, with missing observations kept explicit.
          </p>
        </div>
        <div className="segments" aria-label="Workload kind">
          {["pods", "controllers", "services"].map((t) => (
            <button
              key={t}
              aria-pressed={tab === t}
              onClick={() => {
                setTab(t);
                setFilter("all");
              }}
            >
              {t[0].toUpperCase() + t.slice(1)}
            </button>
          ))}
        </div>
      </div>
      <div className="filters">
        <label className="search">
          <span className="sr-only">Search workloads</span>
          <input
            type="search"
            placeholder="Find a workload…"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
        </label>
        <label>
          State{" "}
          <select
            aria-label="Workload state"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
          >
            <option value="all">All observed states</option>
            <option value="attention">Needs attention / unknown</option>
            {tab === "pods" &&
              (Object.keys(podStates) as PodState[]).map((state) => (
                <option value={state} key={state}>
                  {podStates[state]}
                </option>
              ))}
          </select>
        </label>
      </div>
      {busy && (
        <p role="status" className="muted">
          Loading workload inventory…
        </p>
      )}
      <div className="analytics-scroll">
        <table className="analytics-table workload-table">
          <thead>
            <tr>
              {(tab === "pods"
                ? [
                    "Pod / namespace",
                    "Readiness / scheduling",
                    "Owner",
                    ...(context.tenantMode ? [] : ["Node"]),
                    "Observed usage",
                  ]
                : tab === "controllers"
                  ? [
                      "Controller / namespace",
                      "Kind",
                      "Ready / desired",
                      "Available",
                      "Context",
                    ]
                  : [
                      "Service / namespace",
                      "Type",
                      "Ready endpoints",
                      "Probe result",
                      "Context",
                    ]
              ).map((h) => (
                <th key={h}>{h}</th>
              ))}
            </tr>
          </thead>
          <tbody>
            {tab === "pods" &&
              pods.slice(0, visibleLimit).map((p) => (
                <tr key={p.namespace + "/" + (p.uid || p.name)}>
                  <td>
                    <button
                      className="text-button"
                      onClick={() => onPod({ pod: p, namespace: p.namespace })}
                    >
                      {p.name}
                    </button>
                    <small>{p.namespace}</small>
                  </td>
                  <td>
                    <span className={`state-tag ${p.state}`}>
                      {podStates[p.state]}
                    </span>
                    {p.waitingReasons.length > 0 && (
                      <small>{p.waitingReasons.join(" · ")}</small>
                    )}
                  </td>
                  <td>
                    {p.ownerName || "Unknown"}
                    {p.ownerKind && <small>{p.ownerKind}</small>}
                  </td>
                  {!context.tenantMode && <td>{p.node || "Unknown"}</td>}
                  <td>
                    {numberText(p.cpuCores)} cores
                    <small>{bytesText(p.memoryBytes)}</small>
                  </td>
                </tr>
              ))}
            {tab === "controllers" &&
              controllers.slice(0, visibleLimit).map((c) => (
                <tr key={c.namespace + "/" + c.kind + "/" + c.name}>
                  <td>
                    {c.name}
                    <small>{c.namespace}</small>
                  </td>
                  <td>{c.kind}</td>
                  <td>
                    <span
                      className={
                        controllerNeedsAttention(c) ? "attention-text" : ""
                      }
                    >
                      {numberText(c.ready, 0)} / {numberText(c.desired, 0)}
                    </span>
                  </td>
                  <td>{numberText(c.available, 0)}</td>
                  <td>
                    <a href={workspaceLink("all", c.namespace)}>
                      Namespace alerts →
                    </a>
                  </td>
                </tr>
              ))}
            {tab === "services" &&
              services.slice(0, visibleLimit).map((s) => (
                <tr key={s.namespace + "/" + s.name}>
                  <td>
                    {s.name}
                    <small>{s.namespace}</small>
                  </td>
                  <td>{s.type || "Unknown"}</td>
                  <td>{numberText(s.endpointsReady, 0)}</td>
                  <td>
                    <span
                      className={`state-tag ${s.reachability === "reachable" ? "ready" : s.reachability === "unknown" ? "unknown" : "failed"}`}
                    >
                      {s.reachability === "unknown"
                        ? "Not measured"
                        : s.reachability}
                    </span>
                    {!!s.probeTargets && (
                      <small>
                        {s.failedProbeTargets || 0} failed / {s.probeTargets}{" "}
                        targets
                        {(s.unknownProbeTargets || 0) > 0 &&
                          ` · ${s.unknownProbeTargets} unknown`}
                      </small>
                    )}
                  </td>
                  <td>
                    <a href={workspaceLink("all", s.namespace)}>
                      Namespace alerts →
                    </a>
                  </td>
                </tr>
              ))}
          </tbody>
        </table>
      </div>
      {!busy && (
        <div className="section-head" style={{ marginTop: 16 }}>
          <span className="muted">
            Showing{" "}
            {Math.min(
              visibleLimit,
              tab === "pods"
                ? pods.length
                : tab === "controllers"
                  ? controllers.length
                  : services.length,
            )}{" "}
            of{" "}
            {tab === "pods"
              ? pods.length
              : tab === "controllers"
                ? controllers.length
                : services.length}{" "}
            matching workloads
            {tab === "pods" ? " · attention first, completed last" : ""}
          </span>
          {visibleLimit <
            (tab === "pods"
              ? pods.length
              : tab === "controllers"
                ? controllers.length
                : services.length) && (
            <button onClick={() => setVisibleLimit((n) => n + 100)}>
              Load more workloads
            </button>
          )}
        </div>
      )}
      {!busy &&
        ((tab === "pods" && !pods.length) ||
          (tab === "controllers" && !controllers.length) ||
          (tab === "services" && !services.length)) && (
          <div className="empty">
            {query || filter !== "all"
              ? "No matching workloads. Clear the search or state filter to see more."
              : "No workload inventory observed. Missing metrics do not establish that no workloads exist."}
          </div>
        )}
      <p className="muted enhanced-footnote">
        {tab === "pods"
          ? "Completed jobs are separate from failed pods. A running pod can be not ready; unknown includes missing or stale readiness/scheduling metrics."
          : tab === "controllers"
            ? "Deployment, StatefulSet, and DaemonSet ratios use their own controller metrics. A scaled-down controller can have zero desired replicas."
            : "A Service object and ready endpoints do not prove reachability. Probe results require an explicitly associated monitoring target; “not measured” is not healthy."}
      </p>
    </section>
  );
}
function PodDetail({
  selected,
  incidentsEnabled,
  onClose,
}: {
  selected: { pod: Pod; namespace: string };
  incidentsEnabled: boolean;
  onClose: () => void;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    ref.current?.showModal();
  }, []);
  const p = selected.pod;
  return (
    <dialog ref={ref} className="incident-dialog" onCancel={onClose}>
      <div className="dialog-head">
        <div>
          <div className="eyebrow">Observed pod state</div>
          <h2>{p.name}</h2>
        </div>
        <button onClick={onClose} aria-label="Close pod details">
          ✕
        </button>
      </div>
      <span className={`state-tag ${p.state}`}>{podStates[p.state]}</span>
      <dl className="detail-fields">
        <dt>Namespace</dt>
        <dd>{selected.namespace}</dd>
        <dt>Phase</dt>
        <dd>{p.phase || "Unknown"}</dd>
        <dt>Owner</dt>
        <dd>
          {p.ownerName ? `${p.ownerKind || ""} ${p.ownerName}` : "Unknown"}
        </dd>
        {p.node && (
          <>
            <dt>Node</dt>
            <dd>{p.node}</dd>
          </>
        )}
        <dt>Waiting reasons</dt>
        <dd>
          {p.waitingReasons.length
            ? p.waitingReasons.join(" · ")
            : "None reported"}
        </dd>
        <dt>CPU usage</dt>
        <dd>{numberText(p.cpuCores)} cores</dd>
        <dt>Memory usage</dt>
        <dd>{bytesText(p.memoryBytes)}</dd>
        <dt>Observed</dt>
        <dd>{utcDate(p.observedAt)}</dd>
      </dl>
      <div className="incident-actions">
        <a href={workspaceLink("all", selected.namespace)}>
          Namespace alerts →
        </a>
        {incidentsEnabled && (
          <a href={workspaceLink("incidents", selected.namespace)}>
            Namespace incidents →
          </a>
        )}
      </div>
      <p className="muted enhanced-footnote">
        Links show namespace context, not a claim that every alert concerns this
        pod. Readiness does not establish end-to-end service availability.
      </p>
      <button className="primary" onClick={onClose}>
        Done
      </button>
    </dialog>
  );
}

function CollectionWarnings({
  title,
  warnings,
}: {
  title: string;
  warnings: string[];
}) {
  return (
    <div className="banner context-warning" role="status">
      <div>
        <strong>{title}</strong>
        <span>
          {warnings.length ? ` · ${warnings.length} collection warnings` : ""} ·
          Missing or stale values stay unknown.
        </span>
      </div>
      {warnings.length > 0 && (
        <details>
          <summary>View collection details</summary>
          <ul>
            {warnings.map((warning, index) => (
              <li key={index}>{warning}</li>
            ))}
          </ul>
        </details>
      )}
    </div>
  );
}

function NamespaceQuotas({ rows }: { rows: NamespaceResources[] }) {
  const [search, setSearch] = useState("");
  const [resource, setResource] = useState("");
  const [usage, setUsage] = useState("");
  const quotas = rows.flatMap((r) =>
    (r.quotas || []).map((q) => ({ ...q, namespace: r.namespace })),
  );
  const resources = [...new Set(quotas.map((q) => q.resource))].sort();
  const visible = quotas.filter((q) => {
    const matches = `${q.namespace} ${q.name}`
      .toLowerCase()
      .includes(search.trim().toLowerCase());
    const measured = known(q.used) && known(q.hard);
    const atLimit = measured && q.used! >= q.hard!;
    const nearLimit =
      measured && (q.hard! > 0 ? q.used! / q.hard! >= 0.8 : atLimit);
    return (
      matches &&
      (!resource || q.resource === resource) &&
      (!usage ||
        (usage === "near" && nearLimit) ||
        (usage === "limit" && atLimit) ||
        (usage === "unknown" && !measured))
    );
  });
  return (
    <section className="analytics-panel quota-panel">
      <div className="section-head">
        <h3>Namespace quotas</h3>
        <span className="muted" role="status">
          {visible.length} / {quotas.length} quota entries
        </span>
      </div>
      <p className="muted">
        Allocation accounting, not measured runtime usage. Quotas can overlap
        and are not summed.
      </p>
      <div className="filters">
        <label className="search">
          <span className="sr-only">Search namespace or quota</span>
          <input
            type="search"
            placeholder="Namespace or quota…"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
        </label>
        <label>
          Resource{" "}
          <select
            aria-label="Quota resource"
            value={resource}
            onChange={(e) => setResource(e.target.value)}
          >
            <option value="">All resources</option>
            {resource && !resources.includes(resource) && (
              <option value={resource}>{resource}</option>
            )}
            {resources.map((r) => (
              <option key={r}>{r}</option>
            ))}
          </select>
        </label>
        <label>
          Usage{" "}
          <select
            aria-label="Quota usage"
            value={usage}
            onChange={(e) => setUsage(e.target.value)}
          >
            <option value="">All usage</option>
            <option value="near">80% or higher</option>
            <option value="limit">At or above quota</option>
            <option value="unknown">Unknown usage or quota</option>
          </select>
        </label>
        {(search || resource || usage) && (
          <button
            onClick={() => {
              setSearch("");
              setResource("");
              setUsage("");
            }}
          >
            Clear quota filters
          </button>
        )}
      </div>
      {!!visible.length && (
        <div className="analytics-scroll">
          <table className="analytics-table">
            <thead>
              <tr>
                <th>Namespace / quota</th>
                <th>Resource</th>
                <th>Accounted usage</th>
                <th>Hard quota</th>
                <th>Used / hard</th>
              </tr>
            </thead>
            <tbody>
              {visible.map((q) => (
                <tr key={`${q.namespace}/${q.name}/${q.resource}`}>
                  <td>
                    {q.namespace}
                    <small className="quota-name">{q.name}</small>
                  </td>
                  <td>{q.resource}</td>
                  <td>{quotaValue(q.resource, q.used)}</td>
                  <td>{quotaValue(q.resource, q.hard)}</td>
                  <td>{ratioText(q.used, q.hard)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {!visible.length && (
        <DataState
          label={
            quotas.length
              ? "No matching quota entries"
              : "No quota metrics observed"
          }
          detail={
            quotas.length
              ? "Clear or adjust the quota filters to see more."
              : "This does not establish that no quotas are configured."
          }
        />
      )}
    </section>
  );
}
