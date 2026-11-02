import DataState from "./DataState";
import { useCallback, useEffect, useRef, useState } from "react";
import { api } from "./api";
import { safeURL, type Context } from "./model";
import {
  hoursOpen,
  incidentQueue,
  overdue,
  selectIncidents,
  utcDate,
  type Incident,
  type IncidentResponse,
} from "./incidents";

type Delivery = {
  id: string;
  status: string;
  attempts: number;
  dueAt?: string;
  lastError?: string;
  acceptedAt?: string;
};
export default function IncidentWorkspace({ context }: { context: Context }) {
  const [data, setData] = useState<IncidentResponse>(),
    [error, setError] = useState(""),
    [busy, setBusy] = useState(false),
    [namespace, setNamespace] = useState(
      new URLSearchParams(location.search).get("namespace") || "",
    ),
    [filter, setFilter] = useState("all"),
    [query, setQuery] = useState(
      new URLSearchParams(location.search).get("q") || "",
    ),
    [selected, setSelected] = useState<Incident>(),
    [notice, setNotice] = useState(""),
    [opening, setOpening] = useState(false);
  const lock = useRef(false);
  const loadedPages = useRef(1);
  const deepLink = useRef(new URLSearchParams(location.search).get("incident"));
  useEffect(() => {
    const id = deepLink.current;
    if (!id) return;
    deepLink.current = null;
    setOpening(true);
    void api<Incident>(`incidents/${encodeURIComponent(id)}`)
      .then(setSelected)
      .catch((e) => setError(e.message))
      .finally(() => setOpening(false));
  }, []);
  const refresh = useCallback(async () => {
    if (lock.current) return;
    lock.current = true;
    setBusy(true);
    try {
      let combined: IncidentResponse | undefined;
      for (let page = 0; page < loadedPages.current; page++) {
        const result = await api<IncidentResponse>(
          `incidents?limit=200&offset=${page * 200}`,
        );
        combined = combined
          ? {
              ...result,
              incidents: [...combined.incidents, ...result.incidents],
            }
          : result;
        if (result.incidents.length < 200) break;
      }
      if (combined)
        setData({
          ...combined,
          incidents: [
            ...new Map(combined.incidents.map((i) => [i.id, i])).values(),
          ],
        });
      setError("");
    } catch (e) {
      setError((e as Error).message);
    } finally {
      lock.current = false;
      setBusy(false);
    }
  }, []);
  useEffect(() => {
    void refresh();
    const t = setInterval(() => void refresh(), 30000);
    return () => clearInterval(t);
  }, [refresh]);
  const items = selectIncidents(
    data?.incidents || [],
    namespace,
    filter,
    query,
  );
  const namespaces = [
    ...new Set(
      [
        ...(context.namespaces || []),
        ...(data?.incidents || []).map((i) => i.namespace),
      ].filter(Boolean),
    ),
  ].sort();
  const readOnly = context.readOnly || context.tenantMode || !!data?.readOnly;
  async function loadMore() {
    if (lock.current || !data) return;
    lock.current = true;
    setBusy(true);
    try {
      const result = await api<IncidentResponse>(
        `incidents?limit=200&offset=${loadedPages.current * 200}`,
      );
      loadedPages.current++;
      setData({
        ...result,
        incidents: [
          ...new Map(
            [...data.incidents, ...result.incidents].map((i) => [i.id, i]),
          ).values(),
        ],
      });
      setError("");
    } catch (e) {
      setError((e as Error).message);
    } finally {
      lock.current = false;
      setBusy(false);
    }
  }
  async function open(i: Incident) {
    setOpening(true);
    try {
      setSelected(await api<Incident>(`incidents/${encodeURIComponent(i.id)}`));
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setOpening(false);
    }
  }
  return (
    <section className="enhanced-workspace">
      {opening && <DataState loading label="Loading incident details…" />}
      <div className="section-head">
        <div>
          <h2>What still needs attention.</h2>
          <p className="muted">
            Incident age and follow-up, across namespaces.
          </p>
        </div>
        <button disabled={busy} onClick={() => void refresh()}>
          {busy ? "Refreshing…" : "Refresh incidents"}
        </button>
      </div>
      {error && (
        <div role="alert" className="banner danger">
          <strong>
            {data ? "Incident data is stale" : "Cannot load incidents"}
          </strong>{" "}
          {error}
        </div>
      )}
      {data &&
        data.source.state !== "fresh" &&
        data.source.state !== "ok" &&
        data.source.state !== "connected" && (
          <div className="banner" role="status">
            Source state: {data.source.state} · Last successful observation:{" "}
            {utcDate(data.source.lastSuccess)}. No recovery inferred.
          </div>
        )}
      {notice && (
        <div className="banner" role="status">
          {notice}
        </div>
      )}
      <div className="overview incident-overview">
        {(["now", "unresolved", "monitoring"] as const).map((q, index) => (
          <div
            className={`stat ${q === "now" && items.some((i) => incidentQueue(i) === "now") ? "critical-stat" : ""}`}
            key={q}
          >
            <span>{["Act now", "Unresolved", "Monitoring"][index]}</span>
            <strong>
              {data ? items.filter((i) => incidentQueue(i) === q).length : "—"}
              <small>loaded incidents</small>
            </strong>
          </div>
        ))}
      </div>
      <div className="filters">
        <label className="search">
          <span className="sr-only">Search incidents</span>
          <input
            type="search"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search incidents…"
          />
        </label>
        <label>
          Namespace{" "}
          <select
            value={namespace}
            onChange={(e) => setNamespace(e.target.value)}
          >
            <option value="">
              {context.tenantMode
                ? "All permitted namespaces"
                : "All namespaces"}
            </option>
            {namespaces.map((n) => (
              <option key={n}>{n}</option>
            ))}
          </select>
        </label>
        <label>
          Follow-up{" "}
          <select value={filter} onChange={(e) => setFilter(e.target.value)}>
            <option value="all">All open work</option>
            <option value="overdue">Overdue review</option>
            <option value="unowned">No owner</option>
            <option value="week">Open 7+ days</option>
            <option value="closed">Closed incidents</option>
          </select>
        </label>
      </div>
      {data && (
        <div className="section-head">
          <span className="muted">
            {data.incidents.length} / {data.total ?? data.incidents.length}{" "}
            incidents loaded. Filters and counts apply to loaded incidents.
          </span>
          {data.incidents.length < (data.total ?? data.incidents.length) && (
            <button disabled={busy} onClick={() => void loadMore()}>
              Load more incidents
            </button>
          )}
        </div>
      )}
      {(["now", "unresolved", "monitoring"] as const).map((q, index) => {
        const rows = items.filter((i) => incidentQueue(i) === q);
        const grouped = q !== "now" && rows.length > 7;
        const namespaces = new Map<string, Incident[]>();
        for (const incident of rows) {
          const group = namespaces.get(incident.namespace) || [];
          group.push(incident);
          namespaces.set(incident.namespace, group);
        }
        const renderRows = (entries: Incident[]) =>
          entries.map((i) => (
            <article
              className={
                q === "now" ? "incident-card urgent-incident" : "incident-row"
              }
              key={i.id}
            >
              {q === "now" ? (
                <>
                  <div className="incident-card-top">
                    <span
                      className={
                        i.sourceSeverity === "critical"
                          ? "danger-text"
                          : "muted"
                      }
                    >
                      {i.sourceSeverity || "Unclassified"} source severity
                    </span>
                    <span className="mono">Open {hoursOpen(i.firstSeen)}</span>
                  </div>
                  <h3>{i.title}</h3>
                  <p className="muted">
                    {i.namespace || "Cluster-wide"} · {i.members.length} alert
                    instances
                  </p>
                  <div className="incident-facts">
                    <span>
                      <small>Owner</small>
                      {i.owner || "Unassigned"}
                    </span>
                    <span>
                      <small>Condition</small>
                      {i.clearedAt ? "Cleared · awaiting closure" : "Firing"}
                      {i.closedAt ? " · workflow closed" : ""}
                    </span>
                    <span>
                      <small>Review</small>
                      <span className={overdue(i) ? "danger-text" : ""}>
                        {i.reviewAt
                          ? `${overdue(i) ? "Overdue · " : ""}${utcDate(i.reviewAt)}`
                          : "Not scheduled"}
                      </span>
                    </span>
                  </div>
                  {i.members.some((m) => m.status.state === "suppressed") && (
                    <span className="badge">Contains suppressed alerts</span>
                  )}
                  <div className="incident-card-bottom">
                    <span className="muted">
                      Priority: {i.urgency || "unclassified"}
                      {i.model?.applied ? " · model judgment" : ""}
                    </span>
                    <button
                      className="text-button"
                      onClick={() => void open(i)}
                    >
                      Investigate →
                    </button>
                  </div>
                </>
              ) : (
                <>
                  <div className="incident-row-main">
                    <h3 title={i.title}>{i.title}</h3>
                    <div className="incident-row-meta">
                      <span>{i.namespace || "Cluster-wide"}</span>
                      <span>· {i.members.length} alerts</span>
                      <span
                        className={
                          i.sourceSeverity === "critical" ? "danger-text" : ""
                        }
                      >
                        · {i.sourceSeverity || "Unclassified"}
                      </span>
                      {i.members.some(
                        (m) => m.status.state === "suppressed",
                      ) && <span className="badge">Suppressed</span>}
                      {i.model?.applied && (
                        <span className="badge">Model priority</span>
                      )}
                      {(i.clearedAt || i.closedAt) && (
                        <span className="badge">
                          {i.closedAt ? "Closed" : "Cleared · awaiting closure"}
                        </span>
                      )}
                    </div>
                  </div>
                  <div className="incident-row-owner">
                    <small>Owner</small>
                    <span>{i.owner || "Unassigned"}</span>
                  </div>
                  <div className="incident-row-review">
                    <span>Open {hoursOpen(i.firstSeen)}</span>
                    <small className={overdue(i) ? "danger-text" : ""}>
                      {i.reviewAt
                        ? `${overdue(i) ? "Overdue · " : ""}${utcDate(i.reviewAt)}`
                        : "No review scheduled"}
                    </small>
                  </div>
                  <button className="text-button" onClick={() => void open(i)}>
                    Investigate →
                  </button>
                </>
              )}
            </article>
          ));
        return (
          <section className="incident-section" key={q}>
            <div className="section-head">
              <h3>{["Act now", "Unresolved", "Monitoring"][index]}</h3>
              <span>
                {q === "unresolved"
                  ? "Oldest first · age does not mean ignored"
                  : `${rows.length} incidents`}
              </span>
            </div>
            {grouped ? (
              <div className="incident-namespace-groups">
                {[...namespaces].map(([ns, entries]) => (
                  <details className="incident-namespace" key={ns}>
                    <summary>
                      <strong>{ns || "Cluster-wide"}</strong>
                      <span>
                        {entries.length}{" "}
                        {entries.length === 1 ? "incident" : "incidents"}
                      </span>
                      <span>
                        Oldest{" "}
                        {hoursOpen(
                          entries.reduce(
                            (oldest, i) =>
                              Date.parse(i.firstSeen) < Date.parse(oldest)
                                ? i.firstSeen
                                : oldest,
                            entries[0].firstSeen,
                          ),
                        )}
                      </span>
                      {!!entries.filter(overdue).length && (
                        <span className="danger-text">
                          {entries.filter(overdue).length} overdue
                        </span>
                      )}
                      {!!entries.filter((i) => !i.owner).length && (
                        <span>
                          {entries.filter((i) => !i.owner).length} unassigned
                        </span>
                      )}
                    </summary>
                    <div className="incident-rows">{renderRows(entries)}</div>
                  </details>
                ))}
              </div>
            ) : (
              <div className={q === "now" ? "alert-grid" : "incident-rows"}>
                {renderRows(rows)}
              </div>
            )}
            {!rows.length && (
              <div className="empty">
                {!data && !error
                  ? "Loading incidents…"
                  : !data
                    ? "Incident data unavailable"
                    : q === "now"
                      ? "No matching critical incidents among loaded work. Review unresolved work below."
                      : "No matching incidents."}
              </div>
            )}
          </section>
        );
      })}
      {context.triageEnabled && !context.tenantMode && <TriageStatus />}
      <p className="muted enhanced-footnote">
        Age starts with this application's first observation. Conditions
        clearing and workflow closure are separate. Existing Alertmanager paging
        remains unchanged.
      </p>
      {selected && (
        <IncidentDetail
          incident={selected}
          candidates={data?.incidents || []}
          readOnly={readOnly}
          followUp={!!context.followUpEnabled}
          onClose={() => {
            setSelected(undefined);
            const u = new URL(location.href);
            u.searchParams.delete("incident");
            history.replaceState(null, "", u.pathname + u.search);
          }}
          onSaved={(i) => {
            setSelected(i);
            setNotice(
              "Incident updated. Existing Alertmanager notifications are unchanged.",
            );
            void refresh();
          }}
        />
      )}
    </section>
  );
}
function IncidentDetail({
  incident,
  candidates,
  readOnly,
  followUp,
  onClose,
  onSaved,
}: {
  incident: Incident;
  candidates: Incident[];
  readOnly: boolean;
  followUp: boolean;
  onClose: () => void;
  onSaved: (i: Incident) => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [tab, setTab] = useState("impact"),
    [owner, setOwner] = useState(incident.owner || ""),
    [review, setReview] = useState(
      incident.reviewAt
        ? new Date(incident.reviewAt).toISOString().slice(0, 16)
        : "",
    ),
    [error, setError] = useState(""),
    [saving, setSaving] = useState(false),
    [target, setTarget] = useState(""),
    [member, setMember] = useState(""),
    [deliveries, setDeliveries] = useState<Delivery[]>([]),
    [deliveryError, setDeliveryError] = useState(""),
    [deliveriesLoading, setDeliveriesLoading] = useState(!readOnly && followUp);
  useEffect(() => {
    dialog.current?.showModal();
  }, []);
  useEffect(() => {
    setOwner(incident.owner || "");
    setReview(
      incident.reviewAt
        ? new Date(incident.reviewAt).toISOString().slice(0, 16)
        : "",
    );
  }, [incident]);
  const loadDeliveries = useCallback(() => {
    if (readOnly || !followUp) return;
    setDeliveriesLoading(true);
    void api<Delivery[]>(
      `incidents/${encodeURIComponent(incident.id)}/deliveries`,
    )
      .then((d) => {
        setDeliveries(d || []);
        setDeliveryError("");
      })
      .catch((e) => setDeliveryError(e.message))
      .finally(() => setDeliveriesLoading(false));
  }, [incident.id, readOnly, followUp]);
  useEffect(() => {
    loadDeliveries();
  }, [loadDeliveries]);
  async function mutate(patch: Record<string, unknown>, suffix = "") {
    setSaving(true);
    setError("");
    try {
      const i = await api<Incident>(
        `incidents/${encodeURIComponent(incident.id)}${suffix}`,
        {
          method: "PATCH",
          body: JSON.stringify({ version: incident.version, ...patch }),
        },
      );
      onSaved(i);
    } catch (e) {
      setError(
        `${(e as Error).message}. Reopen the incident if it changed elsewhere.`,
      );
    } finally {
      setSaving(false);
    }
  }
  return (
    <dialog ref={dialog} className="incident-dialog" onCancel={onClose}>
      <div className="dialog-head">
        <div>
          <div className="eyebrow">
            {incident.namespace || "Cluster-wide"} · version {incident.version}
          </div>
          <h2>{incident.title}</h2>
        </div>
        <button onClick={onClose} aria-label="Close incident">
          ✕
        </button>
      </div>
      <nav className="tabs" aria-label="Incident details">
        {["impact", "evidence", "response"].map((t) => (
          <button
            key={t}
            aria-current={tab === t ? "page" : undefined}
            onClick={() => setTab(t)}
          >
            {t[0].toUpperCase() + t.slice(1)}
          </button>
        ))}
      </nav>
      {error && (
        <div role="alert" className="banner danger">
          {error}
        </div>
      )}
      {tab === "impact" && (
        <>
          <dl className="detail-fields">
            <dt>Source severity</dt>
            <dd>{incident.sourceSeverity || "Unclassified"}</dd>
            <dt>Priority</dt>
            <dd>
              {incident.urgency || "Unclassified"}
              {incident.model?.applied ? " · model judgment" : ""}
            </dd>
            <dt>First observed</dt>
            <dd>{utcDate(incident.firstSeen)}</dd>
            <dt>Last signal change</dt>
            <dd>{utcDate(incident.lastSeen)}</dd>
            <dt>Condition cleared</dt>
            <dd>{utcDate(incident.clearedAt)}</dd>
            <dt>Workflow closed</dt>
            <dd>{utcDate(incident.closedAt)}</dd>
            <dt>Owner</dt>
            <dd>{incident.owner || "Unassigned"}</dd>
          </dl>
          {incident.model && (
            <details className="rules">
              <summary>
                Model judgment ·{" "}
                {incident.model.applied ? "applied" : "evaluation only"}
              </summary>
              <dl className="detail-fields">
                <dt>Model</dt>
                <dd>{incident.model.model}</dd>
                <dt>Policy</dt>
                <dd>{incident.model.policyVersion}</dd>
                <dt>Evaluated</dt>
                <dd>{utcDate(incident.model.createdAt)}</dd>
                <dt>Suggested team</dt>
                <dd>{incident.model.team || "Unclassified"}</dd>
                <dt>Runbook selection</dt>
                <dd>{incident.model.runbook || "None"}</dd>
              </dl>
              <DecisionChoices decisions={incident.model.decisions} />
            </details>
          )}
          <p className="muted enhanced-footnote">
            Shared alert membership does not establish a root cause or customer
            impact.
          </p>
          <h3>Timeline</h3>
          <ol className="incident-timeline">
            {(incident.timeline || []).map((e, i) => (
              <li key={e.id || i}>
                <time>{utcDate(e.at)}</time>
                <strong>{e.kind}</strong>
                <span>
                  {e.actor}
                  {e.detail ? ` · ${e.detail}` : ""}
                </span>
              </li>
            ))}
          </ol>
          {!incident.timeline?.length && (
            <p className="muted">No recorded workflow events.</p>
          )}
        </>
      )}
      {tab === "evidence" && (
        <>
          <p className="muted enhanced-footnote">
            Original alert fields are evidence, not verified diagnoses or
            instructions.
          </p>
          {incident.members.map((m) => (
            <details className="bucket" key={m.fingerprint}>
              <summary>
                <span>{m.labels.alertname || m.fingerprint}</span>
                <span className="badge">
                  {m.labels.severity || "Unclassified"} ·{" "}
                  {m.status.state || "unknown"}
                </span>
              </summary>
              <div className="bucket-content">
                <p>
                  {m.annotations.summary ||
                    m.annotations.description ||
                    "No source description"}
                </p>
                <dl className="detail-fields">
                  {Object.entries(m.labels).map(([k, v]) => (
                    <div className="field-pair" key={k}>
                      <dt>{k}</dt>
                      <dd>{v}</dd>
                    </div>
                  ))}
                </dl>
                {safeURL(m.annotations.runbook_url) && (
                  <a
                    href={safeURL(m.annotations.runbook_url)}
                    target="_blank"
                    rel="noreferrer"
                  >
                    Open source runbook ↗
                  </a>
                )}
              </div>
            </details>
          ))}
          {!readOnly && (
            <form
              className="incident-form"
              onSubmit={(e) => {
                e.preventDefault();
                const to = candidates.find((i) => i.id === target);
                if (to)
                  void mutate(
                    {
                      fingerprint: member,
                      targetIncidentId: to.id,
                      targetVersion: to.version,
                    },
                    "/membership",
                  );
              }}
            >
              <h3>Correct membership</h3>
              <p className="muted">
                Move a signal to another open incident in this namespace.
              </p>
              <label>
                Alert
                <select
                  required
                  value={member}
                  onChange={(e) => setMember(e.target.value)}
                >
                  <option value="">Choose an alert</option>
                  {incident.members.map((m) => (
                    <option key={m.fingerprint} value={m.fingerprint}>
                      {m.labels.alertname} · {m.fingerprint}
                    </option>
                  ))}
                </select>
              </label>
              <label>
                Target incident
                <select
                  required
                  value={target}
                  onChange={(e) => setTarget(e.target.value)}
                >
                  <option value="">Choose an incident</option>
                  {candidates
                    .filter(
                      (i) =>
                        i.id !== incident.id &&
                        i.namespace === incident.namespace &&
                        !i.closedAt,
                    )
                    .map((i) => (
                      <option key={i.id} value={i.id}>
                        {i.title} · {i.id}
                      </option>
                    ))}
                </select>
              </label>
              <button
                className="primary"
                disabled={saving || !target || !member}
              >
                Move alert
              </button>
            </form>
          )}
        </>
      )}
      {tab === "response" && (
        <>
          <dl className="detail-fields">
            <dt>Owner</dt>
            <dd>{incident.owner || "Unassigned"}</dd>
            <dt>Acknowledged</dt>
            <dd>{utcDate(incident.acknowledgedAt)}</dd>
            <dt>Review due</dt>
            <dd>{utcDate(incident.reviewAt)}</dd>
          </dl>
          {readOnly ? (
            <p className="muted">
              Read-only access. An operator can update ownership and follow-up.
            </p>
          ) : (
            <>
              <form
                className="incident-form"
                onSubmit={(e) => {
                  e.preventDefault();
                  void mutate({
                    owner,
                    reviewAt: review
                      ? new Date(review + "Z").toISOString()
                      : "",
                  });
                }}
              >
                <label>
                  Owner
                  <input
                    value={owner}
                    onChange={(e) => setOwner(e.target.value)}
                    maxLength={200}
                    placeholder="Person or team"
                  />
                </label>
                <label>
                  Review time (UTC)
                  <input
                    type="datetime-local"
                    value={review}
                    onChange={(e) => setReview(e.target.value)}
                  />
                </label>
                <button disabled={saving} className="primary">
                  Save follow-up
                </button>
              </form>
              <div className="incident-actions">
                <button
                  disabled={saving || !!incident.acknowledgedAt}
                  onClick={() => void mutate({ acknowledge: true })}
                >
                  Acknowledge
                </button>
                <button
                  disabled={
                    saving || !incident.clearedAt || !!incident.closedAt
                  }
                  onClick={() => void mutate({ close: true })}
                >
                  Close cleared incident
                </button>
              </div>
              <p className="muted enhanced-footnote">
                Acknowledging does not cancel the review. Only cleared
                conditions can be closed.
              </p>
            </>
          )}
          {followUp && !readOnly && (
            <>
              <h3>Follow-up deliveries</h3>
              {deliveryError && <p role="alert">{deliveryError}</p>}
              {deliveriesLoading && (
                <DataState loading label="Loading delivery events…" />
              )}
              {!deliveriesLoading && !deliveryError && !deliveries.length && (
                <p className="muted">No delivery events yet.</p>
              )}
              {deliveries.map((d) => (
                <div className="delivery-row" key={d.id}>
                  <span>
                    {d.status === "accepted" || d.status === "sent"
                      ? "Webhook accepted"
                      : d.status}{" "}
                    · {d.attempts} attempts
                  </span>
                  {d.status === "failed" && (
                    <button
                      disabled={saving}
                      onClick={() => {
                        void api(
                          `deliveries/${encodeURIComponent(d.id)}/retry`,
                          { method: "POST", body: "{}" },
                        )
                          .then(loadDeliveries)
                          .catch((e) => setDeliveryError(e.message));
                      }}
                    >
                      Retry
                    </button>
                  )}
                </div>
              ))}
              <p className="muted enhanced-footnote">
                Webhook acceptance is not proof that a person was notified.
              </p>
            </>
          )}
        </>
      )}
    </dialog>
  );
}

function TriageStatus() {
  const [status, setStatus] = useState<{
    enabled: boolean;
    apply: boolean;
    state: string;
    monthlyBudgetUSD: number;
    decisions?: Array<{
      incidentId: string;
      inputHash: string;
      model: string;
      policyVersion: string;
      createdAt: string;
      applied: boolean;
      decisions: unknown;
    }>;
    usage: {
      month: string;
      reservedUSD: number;
      spentUSD: number;
      inputTokens: number;
      requests: number;
    };
  }>();
  const [error, setError] = useState("");
  useEffect(() => {
    let live = true;
    const load = () =>
      void api<typeof status>("triage/status")
        .then((s) => {
          if (live) {
            setStatus(s);
            setError("");
          }
        })
        .catch((e) => {
          if (live) setError(e.message);
        });
    load();
    const timer = setInterval(load, 30000);
    return () => {
      live = false;
      clearInterval(timer);
    };
  }, []);
  return (
    <details className="rules">
      <summary>
        Optional Jev triage ·{" "}
        {error ? "unavailable" : status?.state || "loading"}
      </summary>
      {error && <p role="alert">Cannot load triage usage: {error}</p>}
      {status && (
        <>
          <p>
            {status.apply
              ? "Model grouping and ranking enabled. Source-critical alerts retain their priority."
              : "Evaluation only. Model decisions do not change incident grouping or ranking."}
          </p>
          <dl className="detail-fields">
            <dt>Month</dt>
            <dd>{status.usage.month}</dd>
            <dt>Spent / budget</dt>
            <dd>
              ${status.usage.spentUSD.toFixed(4)} / $
              {status.monthlyBudgetUSD.toFixed(2)}
            </dd>
            <dt>Reserved</dt>
            <dd>${status.usage.reservedUSD.toFixed(4)}</dd>
            <dt>Requests / tokens</dt>
            <dd>
              {status.usage.requests} / {status.usage.inputTokens}
            </dd>
          </dl>
          <h3>Recent evaluations</h3>
          {!status.decisions?.length && (
            <p className="muted">No recorded model evaluations yet.</p>
          )}
          {(status.decisions || []).map((d) => (
            <details key={d.inputHash} className="bucket">
              <summary>
                <span>{d.incidentId}</span>
                <span className="badge">
                  {d.applied ? "Applied" : "Evaluation only"}
                </span>
              </summary>
              <div className="bucket-content">
                <p className="muted">
                  {utcDate(d.createdAt)} · {d.model} · policy {d.policyVersion}
                </p>
                <DecisionChoices decisions={d.decisions} />
                <a
                  href={`?view=incidents&incident=${encodeURIComponent(d.incidentId)}`}
                >
                  Inspect current incident evidence →
                </a>
                <p className="muted enhanced-footnote">
                  Current evidence may differ from the snapshot evaluated by
                  this decision.
                </p>
              </div>
            </details>
          ))}
          <p>Existing Alertmanager notification routing is unchanged.</p>
        </>
      )}
    </details>
  );
}

function DecisionChoices({ decisions }: { decisions: unknown }) {
  if (!decisions || typeof decisions !== "object")
    return <p className="muted">Decision details unavailable.</p>;
  const labels: Record<string, string> = {
    urgency: "Urgency",
    membership: "Same-namespace incident",
    related: "Possible cross-namespace relationship",
    team: "Suggested team",
    runbook: "Runbook selection",
  };
  return (
    <dl className="detail-fields">
      {Object.entries(labels).flatMap(([key, label]) => {
        const answer = (decisions as Record<string, unknown>)[key];
        if (
          !answer ||
          typeof answer !== "object" ||
          typeof (answer as { choice?: unknown }).choice !== "string"
        )
          return [];
        return [
          <div className="field-pair" key={key}>
            <dt>{label}</dt>
            <dd>{(answer as { choice: string }).choice}</dd>
          </div>,
        ];
      })}
    </dl>
  );
}
