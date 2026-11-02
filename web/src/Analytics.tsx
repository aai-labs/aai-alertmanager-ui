import { useEffect, useState } from "react";
import { api } from "./api";
import type { Context } from "./model";
import {
  percent,
  dayDescription,
  heatLevel,
  type AnalyticsResponse,
  type NamespaceAnalytics,
} from "./analytics";
export default function Analytics({ context }: { context: Context }) {
  const [days, setDays] = useState("7"),
    [namespace, setNamespace] = useState(
      new URLSearchParams(location.search).get("namespace") || "",
    ),
    [severity, setSeverity] = useState("actionable"),
    [data, setData] = useState<AnalyticsResponse>(),
    [selected, setSelected] = useState(""),
    [error, setError] = useState(""),
    [loading, setLoading] = useState(true),
    [revision, setRevision] = useState(0);
  useEffect(() => {
    let active = true;
    setLoading(true);
    setData(undefined);
    setError("");
    const params = new URLSearchParams({ days, severity });
    if (namespace) params.set("namespace", namespace);
    api<AnalyticsResponse>(`analytics?${params}`, undefined, 25000)
      .then((v) => {
        if (active) {
          setData(v);
          setSelected((old) =>
            v.namespaces.some((n) => n.namespace === old)
              ? old
              : v.namespaces[0]?.namespace || "",
          );
        }
      })
      .catch((e) => {
        if (active) setError(e.message);
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, [days, namespace, severity, revision]);
  const [knownNamespaces, setKnownNamespaces] = useState<string[]>(
    context.namespaces || [],
  );
  useEffect(() => {
    if (data)
      setKnownNamespaces((old) =>
        [
          ...new Set([
            ...old,
            ...data.namespaces.map((n) => n.namespace).filter(Boolean),
          ]),
        ].sort(),
      );
  }, [data]);
  const selectedRow = data?.namespaces.find((n) => n.namespace === selected);
  return (
    <section className="enhanced-workspace">
      <div className="section-head">
        <div>
          <h2>Where alerts keep coming back.</h2>
          <p className="muted">
            Namespace activity · observed Prometheus history
          </p>
        </div>
        <button disabled={loading} onClick={() => setRevision((v) => v + 1)}>
          Refresh history
        </button>
      </div>
      <div className="filters">
        <label>
          Window
          <select value={days} onChange={(e) => setDays(e.target.value)}>
            <option value="7">Last 7 days</option>
            <option value="30">Last 30 days</option>
          </select>
        </label>
        <label>
          Namespace
          <select
            value={namespace}
            onChange={(e) => setNamespace(e.target.value)}
          >
            <option value="">
              {context.tenantMode
                ? "All permitted namespaces"
                : "All namespaces"}
            </option>
            {knownNamespaces.map((n) => (
              <option key={n}>{n}</option>
            ))}
          </select>
        </label>
        <label>
          Severity
          <select
            value={severity}
            onChange={(e) => setSeverity(e.target.value)}
          >
            <option value="actionable">Exclude informational</option>
            <option value="all">All severities</option>
            <option value="critical">Critical</option>
            <option value="warning">Warning</option>
            <option value="info">Informational</option>
          </select>
        </label>
      </div>
      {loading && (
        <div className="empty" role="status">
          Loading bounded history queries…
        </div>
      )}
      {error && (
        <div className="banner danger" role="alert">
          Cannot load analytics: {error}
        </div>
      )}
      {data && (
        <>
          {data.incomplete && (
            <div className="banner" role="status">
              <strong>Incomplete history</strong>
              <span>
                {data.warnings.join(" · ") ||
                  "Some periods have no confirmed source coverage."}
              </span>
            </div>
          )}
          <div className="overview incident-overview">
            <div className="stat">
              <span>Observed new episodes</span>
              <strong>
                {data.namespaces.reduce((sum, n) => sum + n.episodes, 0)}
              </strong>
            </div>
            <div className="stat">
              <span>Namespaces with observed alerts</span>
              <strong>
                {data.namespaces.filter((n) => n.firingSeconds > 0).length}
              </strong>
            </div>
            <div className="stat">
              <span>Source coverage</span>
              <strong>{percent(data.coverage.percent)}</strong>
            </div>
          </div>
          <p className="muted enhanced-footnote">
            {data.start.slice(0, 10)} – {data.end.slice(0, 10)} · UTC ·{" "}
            {data.resolutionSeconds}-second samples · current day may be partial
          </p>
          <section className="analytics-panel">
            <div className="section-head">
              <div>
                <h3>How often are alerts present?</h3>
                <p className="muted">
                  Each cell is one UTC day. Darker means more of the covered
                  time had firing alerts.
                </p>
              </div>
            </div>
            <div className="heat-legend">
              <span className="heat-cell none" />
              0% <span className="heat-cell low" />
              &lt;25% <span className="heat-cell medium" />
              &lt;75% <span className="heat-cell high" />
              75–100% <span className="heat-cell unknown" />
              Unknown
            </div>
            <div className="analytics-scroll">
              <div
                className="heatmap"
                style={{ minWidth: Number(days) === 30 ? 1160 : 520 }}
              >
                {!!data.namespaces.length && (
                  <div
                    className="heat-row heat-date-header"
                    style={{
                      gridTemplateColumns: `170px repeat(${data.namespaces[0].days.length}, minmax(12px,1fr)) 65px`,
                    }}
                  >
                    <span>Namespace</span>
                    {data.namespaces[0].days.map(({ date }) => (
                      <time
                        key={date}
                        dateTime={date}
                        title={`${date} · UTC`}
                        aria-label={`${date} UTC`}
                      >
                        <span>
                          {new Date(`${date}T00:00:00Z`).toLocaleDateString(
                            "en-GB",
                            { month: "short", timeZone: "UTC" },
                          )}
                        </span>
                        <strong>{date.slice(8, 10)}</strong>
                      </time>
                    ))}
                    <span>Firing %</span>
                  </div>
                )}
                {data.namespaces.map((n) => (
                  <div
                    className="heat-row"
                    key={n.namespace}
                    style={{
                      gridTemplateColumns: `170px repeat(${n.days.length}, minmax(12px,1fr)) 65px`,
                    }}
                  >
                    <button
                      className="text-button"
                      aria-pressed={selected === n.namespace}
                      onClick={() => setSelected(n.namespace)}
                    >
                      {n.namespace || "Cluster-wide"}
                    </button>
                    {n.days.map((d) => (
                      <button
                        key={d.date}
                        className={`heat-cell ${heatLevel(d)} ${d.knownSeconds < d.expectedSeconds ? "partial" : ""}`}
                        title={dayDescription(d)}
                        aria-label={`${n.namespace || "Cluster-wide"}, ${dayDescription(d)}`}
                        onClick={() => setSelected(n.namespace)}
                      />
                    ))}
                    <span>{percent(n.firingPercent)}</span>
                  </div>
                ))}
              </div>
            </div>
            {!data.namespaces.length && (
              <div className="empty">
                No matching alert series were observed. This does not establish
                namespace health.
              </div>
            )}
            <p className="muted enhanced-footnote">
              Striped cells have incomplete source coverage. Select a namespace
              to inspect daily values.
            </p>
          </section>
          {selectedRow && (
            <section className="analytics-panel">
              <div className="section-head">
                <div>
                  <h3>{selectedRow.namespace || "Cluster-wide"}</h3>
                  <p className="muted">
                    {(selectedRow.firingSeconds / 3600).toFixed(1)} firing hours
                    · {selectedRow.episodes} observed new episodes
                  </p>
                </div>
              </div>
              <DailyChart row={selectedRow} />
              <details>
                <summary>Accessible daily data</summary>
                <div className="analytics-scroll">
                  <table className="analytics-table">
                    <thead>
                      <tr>
                        <th>Date (UTC)</th>
                        <th>Hours firing</th>
                        <th>New episodes</th>
                        <th>Hours covered</th>
                      </tr>
                    </thead>
                    <tbody>
                      {selectedRow.days.map((d) => (
                        <tr key={d.date}>
                          <td>{d.date.slice(0, 10)}</td>
                          <td>
                            {d.knownSeconds
                              ? (d.firingSeconds / 3600).toFixed(2)
                              : "Unknown"}
                          </td>
                          <td>{d.knownSeconds ? d.episodes : "Unknown"}</td>
                          <td>
                            {(d.knownSeconds / 3600).toFixed(2)} /{" "}
                            {(d.expectedSeconds / 3600).toFixed(2)}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </details>
            </section>
          )}
          <section className="analytics-panel">
            <h3>Namespace comparison</h3>
            <div className="analytics-scroll">
              <table className="analytics-table">
                <thead>
                  <tr>
                    <th>Namespace</th>
                    <th>Covered time with alerts</th>
                    <th>New episodes</th>
                    <th>Days with alerts</th>
                    <th>Covered hours</th>
                  </tr>
                </thead>
                <tbody>
                  {data.namespaces.map((n) => (
                    <tr key={n.namespace}>
                      <td>
                        <button
                          className="text-button"
                          onClick={() => setSelected(n.namespace)}
                        >
                          {n.namespace || "Cluster-wide"}
                        </button>
                      </td>
                      <td>{percent(n.firingPercent)}</td>
                      <td>{n.episodes}</td>
                      <td>{n.daysWithAlerts}</td>
                      <td>{(n.knownSeconds / 3600).toFixed(1)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </section>
        </>
      )}
      <details className="rules">
        <summary>How these measurements work</summary>
        <p>
          Firing time counts overlapping alerts once per namespace. An episode
          is an observed transition into firing for a distinct alert label set.
          An alert already firing at the start of the window is not a new
          episode. Silenced alerts remain included while their conditions fire.
        </p>
        <p>
          Source coverage measures Prometheus availability, not whether every
          service was monitored. Gaps are unknown. Brief episodes may fall
          between samples and available retention may be shorter than the
          requested window. Namespaces without observed alert series may not
          appear.
        </p>
        <p>
          Alert activity is not an uptime or stability score: different alert
          rules and monitoring coverage affect comparisons. All daily boundaries
          are UTC.
        </p>
      </details>
    </section>
  );
}
function DailyChart({ row }: { row: NamespaceAnalytics }) {
  const width = 1000,
    left = 48,
    step = (width - left - 20) / Math.max(1, row.days.length),
    maxEpisodes = Math.max(4, ...row.days.map((d) => d.episodes));
  function plot(kind: "hours" | "episodes", top: number) {
    const max = kind === "hours" ? 24 : maxEpisodes;
    return (
      <g>
        <text x="0" y={top - 13} className="chart-label">
          {kind === "hours" ? "Hours with alerts" : "New episodes"}
        </text>
        {[0, max / 2, max].map((v) => (
          <g key={v}>
            <line
              x1={left}
              x2="980"
              y1={top + 100 - (v / max) * 100}
              y2={top + 100 - (v / max) * 100}
              className="chart-grid"
            />
            <text x="4" y={top + 104 - (v / max) * 100} className="chart-label">
              {Number(v.toFixed(1))}
            </text>
          </g>
        ))}
        {row.days.map((d, i) => {
          const value = kind === "hours" ? d.firingSeconds / 3600 : d.episodes;
          return (
            <g key={d.date}>
              {!d.knownSeconds ? (
                <rect
                  x={left + i * step + 2}
                  y={top}
                  width={Math.max(2, step - 5)}
                  height="100"
                  fill="url(#chart-unknown)"
                />
              ) : (
                <rect
                  x={left + i * step + 2}
                  y={top + 100 - (value / max) * 100}
                  width={Math.max(2, step - 5)}
                  height={(value / max) * 100}
                  rx="2"
                  fill={kind === "hours" ? "#0000f0" : "#997444"}
                />
              )}
              <title>{dayDescription(d)}</title>
            </g>
          );
        })}
      </g>
    );
  }
  return (
    <svg
      viewBox="0 0 1000 340"
      className="analytics-chart"
      role="img"
      aria-label={`${row.namespace || "Cluster-wide"}: daily firing hours and new episodes. ${row.episodes} observed new episodes. Exact values are in the accessible daily data table.`}
    >
      <defs>
        <pattern
          id="chart-unknown"
          width="6"
          height="6"
          patternUnits="userSpaceOnUse"
        >
          <path d="M0 6L6 0" stroke="#d1d1df" strokeWidth="2" />
        </pattern>
      </defs>
      {plot("hours", 25)}
      {plot("episodes", 190)}
      <text x="48" y="330" className="chart-label">
        {row.days[0]?.date.slice(0, 10)} UTC
      </text>
      <text x="980" y="330" textAnchor="end" className="chart-label">
        {row.days.at(-1)?.date.slice(0, 10)} UTC
      </text>
    </svg>
  );
}
