import { useCallback, useEffect, useRef, useState } from "react";
import { api } from "./api";
import {
  age,
  alertState,
  classify,
  enrich,
  groupAlerts,
  normalizeAlerts,
  pendingAlerts,
  resource,
  safeURL,
  searchAlerts,
  secondaryCategories,
  type Alert,
  type Classified,
  type Context,
  type Group,
  type PromAlert,
  type Silence,
} from "./model";
import SilenceEditor from "./SilenceEditor";

export default function App() {
  const params = new URLSearchParams(location.search);
  const [view, setView] = useState(
    ["focus", "all", "silences"].includes(params.get("view") || "")
      ? params.get("view")!
      : "focus",
  );
  const [namespace, setNamespace] = useState(params.get("namespace") || "");
  const [query, setQuery] = useState(params.get("q") || "");
  const [context, setContext] = useState<Context>();
  const [alerts, setAlerts] = useState<Alert[]>([]),
    [prom, setProm] = useState<PromAlert[]>([]),
    [silences, setSilences] = useState<Silence[]>([]);
  const [last, setLast] = useState<Date>(),
    [busy, setBusy] = useState(false),
    [error, setError] = useState(""),
    [promError, setPromError] = useState(""),
    [silenceError, setSilenceError] = useState("");
  const [editor, setEditor] = useState<Silence | null>(null),
    [expire, setExpire] = useState<Silence | null>(null),
    [notice, setNotice] = useState("");
  const lock = useRef(false),
    identity = useRef("");
  const refresh = useCallback(async () => {
    if (lock.current) return;
    lock.current = true;
    setBusy(true);
    try {
      const ctx = await api<Context>("context");
      const key = JSON.stringify([ctx.identity, ctx.namespaces]);
      if (identity.current !== key) {
        setAlerts([]);
        setProm([]);
        setSilences([]);
        setLast(undefined);
        identity.current = key;
      }
      setContext(ctx);
      if (ctx.tenantMode)
        setView((current) => (current === "silences" ? "focus" : current));
      const [a, p, s] = await Promise.allSettled([
        api<Alert[]>("alerts"),
        ctx.prometheusEnabled
          ? api<{ data: { alerts: PromAlert[] } }>("prometheus")
          : Promise.resolve(null),
        !ctx.tenantMode ? api<Silence[]>("silences") : Promise.resolve([]),
      ]);
      if (a.status === "fulfilled") {
        setAlerts(normalizeAlerts(a.value || []));
        setLast(new Date());
        setError("");
      } else setError(a.reason.message);
      if (p.status === "fulfilled") {
        setProm(p.value?.data.alerts || []);
        setPromError("");
      } else {
        setProm([]);
        setPromError("Prometheus unavailable · enrichment paused");
      }
      if (s.status === "fulfilled") {
        setSilences(s.value || []);
        setSilenceError("");
      } else {
        setSilences([]);
        setSilenceError(s.reason.message);
      }
    } catch (e) {
      setContext(undefined);
      setAlerts([]);
      setProm([]);
      setSilences([]);
      setLast(undefined);
      setError((e as Error).message);
    } finally {
      lock.current = false;
      setBusy(false);
    }
  }, []);
  useEffect(() => {
    void refresh();
    const timer = setInterval(() => void refresh(), 30000);
    return () => clearInterval(timer);
  }, [refresh]);
  useEffect(() => {
    const p = new URLSearchParams();
    if (view !== "focus") p.set("view", view);
    if (namespace) p.set("namespace", namespace);
    if (query) p.set("q", query);
    history.replaceState(
      null,
      "",
      location.pathname + (p.size ? "?" + p.toString() : ""),
    );
  }, [view, namespace, query]);
  const all = [...enrich(alerts, prom), ...pendingAlerts(prom)].map((a) =>
    classify(a, context?.priorities || []),
  );
  const visible = searchAlerts(all, namespace, query),
    primary = visible.filter((a) => a.primary),
    groups = groupAlerts(view === "all" ? visible : primary);
  const namespaces = [
    ...new Set(all.map((a) => a.labels.namespace).filter(Boolean)),
  ].sort();
  const critical = visible.filter(
    (a) =>
      a.labels.severity === "critical" &&
      !a.pending &&
      a.status.state === "active",
  ).length;
  const suppressed = visible.filter((a) => a.status.state === "suppressed");
  function newSilence(a?: Alert) {
    setNotice("");
    setEditor({
      matchers: a
        ? Object.entries(a.labels)
            .filter(([k]) => ["alertname", "namespace"].includes(k))
            .map(([name, value]) => ({
              name,
              value,
              isRegex: false,
              isEqual: true,
            }))
        : [{ name: "alertname", value: "", isRegex: false, isEqual: true }],
      startsAt: new Date().toISOString(),
      endsAt: new Date(Date.now() + 2 * 3600000).toISOString(),
      createdBy: context?.identity || "",
      comment: "",
    });
  }
  return (
    <div className="shell">
      <header className="top">
        <div>
          <div className="eyebrow">ALERTMANAGER UI</div>
          <h1>
            Alerts<span className="brand-dot">.</span>
          </h1>
        </div>
        <div className="header-right">
          {context?.demo && (
            <span className="badge">Demo · synthetic data</span>
          )}
          {context?.tenantMode && (
            <span className="badge">{context.identity} · read only</span>
          )}
          {context?.readOnly && !context.tenantMode && !context.demo && (
            <span className="badge">Read only</span>
          )}
          <div className="refresh">
            <span className="timestamp">
              {last
                ? `Updated ${last.toLocaleTimeString()}`
                : "Waiting for Alertmanager"}
            </span>
            <button
              onClick={() => void refresh()}
              disabled={busy}
              aria-label="Refresh alerts"
            >
              {busy ? "Refreshing…" : "↻ Refresh"}
            </button>
          </div>
        </div>
      </header>
      {error && (
        <div className="banner danger" role="alert">
          <strong>{last ? "Alert data is stale" : "Cannot load alerts"}</strong>
          <span>
            {error}
            {last ? " · showing last successful response" : ""}
          </span>
        </div>
      )}
      {promError && (
        <div className="banner" role="status">
          {promError}
        </div>
      )}
      {notice && (
        <div className="banner" role="status">
          {notice}
        </div>
      )}
      <nav className="tabs" aria-label="Views">
        {[
          ["focus", "Needs attention"],
          ["all", "All alerts"],
          ...(!context?.tenantMode ? [["silences", "Silences"]] : []),
        ].map(([id, label]) => (
          <button
            key={id}
            aria-current={view === id ? "page" : undefined}
            onClick={() => setView(id)}
          >
            {label}
            <span>
              {id === "silences"
                ? silences.filter((s) => s.status?.state === "active").length
                : id === "all"
                  ? all.length
                  : all.filter((a) => a.primary).length}
            </span>
          </button>
        ))}
      </nav>
      {view !== "silences" ? (
        <>
          <section className="overview" aria-label="Alert counts">
            <div className={critical ? "stat critical-stat" : "stat"}>
              <span>Active critical</span>
              <strong>
                {critical}
                <small>alerts</small>
              </strong>
            </div>
            <div className="stat">
              <span>Needs attention</span>
              <strong>
                {primary.length}
                <small>alerts</small>
              </strong>
            </div>
            <div className="stat">
              <span>Suppressed</span>
              <strong>
                {suppressed.length}
                <small>alerts</small>
              </strong>
            </div>
            <div className="stat">
              <span>Pending</span>
              <strong>
                {context?.prometheusEnabled && !promError
                  ? visible.filter((a) => a.pending).length
                  : "—"}
                <small>
                  {promError
                    ? "unavailable"
                    : context?.prometheusEnabled
                      ? "rules"
                      : "not connected"}
                </small>
              </strong>
            </div>
          </section>
          <div className="filters">
            <label className="search">
              <span className="sr-only">Search alerts or labels</span>
              <input
                type="search"
                placeholder="Search alerts or labels…"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
              />
              <span aria-hidden="true">⌕</span>
            </label>
            <label className="namespace-label">
              Namespace
              <select
                value={namespace}
                onChange={(e) => setNamespace(e.target.value)}
              >
                <option value="">
                  {context?.tenantMode
                    ? "All permitted namespaces"
                    : "All namespaces"}
                </option>
                {!context?.tenantMode && (
                  <option value="__cluster__">Cluster-wide</option>
                )}
                {namespace &&
                  !namespaces.includes(namespace) &&
                  namespace !== "__cluster__" && (
                    <option value={namespace}>{namespace}</option>
                  )}
                {namespaces.map((n) => (
                  <option key={n}>{n}</option>
                ))}
              </select>
            </label>
            {(query || namespace) && (
              <button
                className="text-button"
                onClick={() => {
                  setQuery("");
                  setNamespace("");
                }}
              >
                Clear filters
              </button>
            )}
          </div>
          {!context?.tenantMode && (
            <div className="core-strip">
              <span>
                <span className="square" />
                Core infrastructure{" "}
                <strong>
                  {
                    visible.filter((a) => a.category === "Core infrastructure")
                      .length
                  }
                </strong>
              </span>
              <span>
                {suppressed.some((a) => a.labels.severity === "critical")
                  ? `${suppressed.filter((a) => a.labels.severity === "critical").length} suppressed critical · all categories`
                  : "Alert state only · not a health check"}
              </span>
            </div>
          )}
          <section className="queue">
            <div className="section-head">
              <h2>{view === "all" ? "All alerts" : "Needs attention"}</h2>
              <span>
                {view === "all" ? visible.length : primary.length} alerts ·{" "}
                {groups.length} groups
              </span>
            </div>
            <div className="alert-grid">
              {groups.map((g) => (
                <AlertCard
                  key={g.key}
                  group={g}
                  onSilence={
                    context && !context.readOnly && !context.tenantMode
                      ? newSilence
                      : undefined
                  }
                />
              ))}
            </div>
            {!groups.length && (
              <div className="empty">
                {busy && !last
                  ? "Loading alerts…"
                  : error && !last
                    ? "Alert data unavailable"
                    : `No matching ${view === "all" ? "alerts" : "priority alerts"}`}
              </div>
            )}
          </section>
          {view !== "all" && (
            <section className="secondary" aria-label="Other alerts">
              {secondaryCategories.map((category) => {
                const items = visible.filter((a) => a.category === category);
                return (
                  <details className="bucket" key={category}>
                    <summary>
                      <span>{category}</span>
                      <span className="bucket-count">
                        {items.length}
                        <span className="chevron">›</span>
                      </span>
                    </summary>
                    <div className="bucket-content">
                      <div className="alert-grid">
                        {groupAlerts(items).map((g) => (
                          <AlertCard
                            key={g.key}
                            group={g}
                            onSilence={
                              context &&
                              !context.readOnly &&
                              !context.tenantMode
                                ? newSilence
                                : undefined
                            }
                          />
                        ))}
                      </div>
                      {!items.length && (
                        <p className="muted">
                          {category === "Pending" && !context?.prometheusEnabled
                            ? "Connect Prometheus to show pending rules."
                            : "No matching alerts."}
                        </p>
                      )}
                    </div>
                  </details>
                );
              })}
            </section>
          )}
          <details className="rules">
            <summary>How alerts are prioritized</summary>
            <p>
              Critical alerts and recognized infrastructure or availability
              alerts appear first. Unfamiliar alerts stay visible. Routine
              categories and suppressed alerts are collapsed; suppressed
              critical infrastructure stays visible. Groups share a namespace
              and category, not an inferred cause.
            </p>
            <p>
              Expand an alert to see its matching rule. Optional configuration
              can change categories and names.
            </p>
          </details>
        </>
      ) : (
        <section className="queue">
          <div className="section-head">
            <h2>Silences</h2>
            {context && !context.readOnly && !context.tenantMode && (
              <button className="primary" onClick={() => newSilence()}>
                ＋ New silence
              </button>
            )}
          </div>
          {silenceError && (
            <div className="banner danger" role="alert">
              {silenceError}
            </div>
          )}
          <div className="silence-list">
            {silences
              .slice()
              .sort((a, b) => Date.parse(b.startsAt) - Date.parse(a.startsAt))
              .map((s) => (
                <article className="silence-card" key={s.id}>
                  <div className="section-head">
                    <span
                      className={
                        "badge " +
                        (s.status?.state === "active" ? "active" : "")
                      }
                    >
                      {s.status?.state || "unknown"}
                    </span>
                    <span className="mono">{s.createdBy}</span>
                  </div>
                  <h3>{s.comment || "No comment"}</h3>
                  <div className="matcher-chips">
                    {s.matchers.map((m, i) => (
                      <code key={i}>
                        {m.name}
                        {m.isEqual === false ? "!" : "="}
                        {m.isRegex ? "~" : m.isEqual === false ? "=" : ""}"
                        {m.value}"
                      </code>
                    ))}
                  </div>
                  <div className="silence-bottom">
                    <span>
                      {new Date(s.startsAt).toLocaleString()} →{" "}
                      {new Date(s.endsAt).toLocaleString()}
                    </span>
                    {context && !context.readOnly && !context.tenantMode && (
                      <div>
                        <button onClick={() => setEditor(s)}>Edit</button>
                        {s.status?.state !== "expired" && (
                          <button
                            className="danger-text"
                            onClick={() => setExpire(s)}
                          >
                            Expire
                          </button>
                        )}
                      </div>
                    )}
                  </div>
                </article>
              ))}
          </div>
          {!silences.length && !silenceError && (
            <div className="empty">No silences</div>
          )}
        </section>
      )}
      <footer>
        <span>
          Alertmanager UI ·{" "}
          {context?.tenantMode
            ? "Namespace access enforced by server"
            : "Deterministic priorities · no AI"}
        </span>
        <span>Refreshes every 30 seconds</span>
      </footer>
      {editor && (
        <SilenceEditor
          initial={editor}
          onClose={() => setEditor(null)}
          onSaved={() => {
            setEditor(null);
            setNotice("Silence saved.");
            void refresh();
          }}
        />
      )}
      {expire && (
        <ExpireDialog
          silence={expire}
          onClose={() => setExpire(null)}
          onSaved={() => {
            setExpire(null);
            setNotice("Silence expired.");
            void refresh();
          }}
        />
      )}
    </div>
  );
}
function AlertCard({
  group: g,
  onSilence,
}: {
  group: Group;
  onSilence?: (a: Alert) => void;
}) {
  return (
    <details className={"alert-card " + (g.critical ? "critical" : "")}>
      <summary>
        <div className="card-meta">
          <span>{g.category}</span>
          {g.critical && (
            <span className="severity critical-text">critical</span>
          )}
          {g.alerts.some((a) => a.status.state === "suppressed") && (
            <span className="severity">suppressed</span>
          )}
        </div>
        <div className="card-main">
          <h3>{g.namespace}</h3>
          <span className="card-count">
            {g.alerts.length}
            <small>{g.alerts.length === 1 ? "alert" : "alerts"}</small>
          </span>
        </div>
        <div className="card-bottom">
          <span>
            {[...new Set(g.alerts.map((a) => a.displayName))].join(" · ")}
          </span>
          <span className="chevron" aria-hidden="true">
            ›
          </span>
        </div>
      </summary>
      <div className="card-content">
        {g.alerts.map((a) => (
          <AlertDetail key={a.fingerprint} alert={a} onSilence={onSilence} />
        ))}
      </div>
    </details>
  );
}
function AlertDetail({
  alert: a,
  onSilence,
}: {
  alert: Classified;
  onSilence?: (a: Alert) => void;
}) {
  const url = safeURL(a.annotations.runbook_url);
  return (
    <article className="instance">
      <div className="instance-head">
        <strong>{a.displayName}</strong>
        <span
          className={
            "mono " + (a.labels.severity === "critical" ? "critical-text" : "")
          }
        >
          {a.labels.severity || "unknown"} · {alertState(a)}
        </span>
      </div>
      <div className="resource">{resource(a)}</div>
      <div className="instance-meta">
        <span>
          {a.pending ? "Pending for" : "Alert age"}: {age(a.startsAt)}
        </span>
        {a.value !== undefined && <span>Rule value: {a.value}</span>}
        {url && (
          <a href={url} target="_blank" rel="noreferrer">
            Runbook ↗
          </a>
        )}
        {onSilence && !a.pending && (
          <button className="text-button" onClick={() => onSilence(a)}>
            Silence…
          </button>
        )}
      </div>
      <details className="source">
        <summary>
          Source fields <span>{a.reason}</span>
        </summary>
        {a.annotations.summary && <p>{a.annotations.summary}</p>}
        <pre>
          {JSON.stringify(
            {
              labels: a.labels,
              annotations: a.annotations,
              startsAt: a.startsAt,
              status: a.status,
            },
            null,
            2,
          )}
        </pre>
      </details>
    </article>
  );
}
function ExpireDialog({
  silence,
  onClose,
  onSaved,
}: {
  silence: Silence;
  onClose: () => void;
  onSaved: () => void;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  const [error, setError] = useState(""),
    [busy, setBusy] = useState(false);
  useEffect(() => {
    ref.current?.showModal();
  }, []);
  return (
    <dialog ref={ref} onCancel={onClose}>
      <div className="dialog-head">
        <h2>Expire this silence?</h2>
        <button onClick={onClose} aria-label="Close">
          ×
        </button>
      </div>
      <p>Matching alerts may send notifications again.</p>
      <p className="muted">{silence.comment}</p>
      {error && (
        <p role="alert" className="critical-text">
          {error}
        </p>
      )}
      <div className="dialog-actions">
        <button onClick={onClose}>Cancel</button>
        <button
          className="primary"
          disabled={busy}
          onClick={async () => {
            setBusy(true);
            try {
              await api("silences/" + encodeURIComponent(silence.id!), {
                method: "DELETE",
              });
              onSaved();
            } catch (e) {
              setError((e as Error).message);
            } finally {
              setBusy(false);
            }
          }}
        >
          {busy ? "Expiring…" : "Expire silence"}
        </button>
      </div>
    </dialog>
  );
}
