import { useEffect, useMemo, useState } from "react";

const API = "http://localhost:8080";
const PAGE_SIZE = 25;

const STATUS = {
  Scheduled: 0,
  Unscheduled: 1,
  Cancelled: 2,
};

const ORIGINAL_UNSCHEDULED_REASONS = new Set([
  "no room available in wave",
  "ran out of interview time in wave",
]);

const DAY_OPTIONS = [
  { value: "", label: "Select when..." },
  { value: "2026-09-01T09:00", label: "Day 1 AM wave (Sep 1, 09:00)" },
  { value: "2026-09-01T13:00", label: "Day 1 PM wave (Sep 1, 13:00)" },
  { value: "2026-09-01T00:00", label: "Day 1 full day (Sep 1, all)" },
  { value: "2026-09-02T09:00", label: "Day 2 (Sep 2, 09:00)" },
  { value: "2026-09-03T09:00", label: "Day 3 (Sep 3, 09:00)" },
  { value: "2026-09-04T09:00", label: "Day 4 (Sep 4, 09:00)" },
];

const KIND_FIELDS = {
  student_dropout: ["student_id"],
  panel_dropout: ["panel_id"],
  late_company: ["company_id", "delay_minutes"],
  room_unavailable: ["room_id"],
};

const FIELD_META = {
  student_id: { label: "Student", placeholder: "Type or pick S0001", list: "student-options" },
  panel_id: { label: "Panel", placeholder: "Type or pick P-C03-01", list: "panel-options" },
  company_id: { label: "Company", placeholder: "Type or pick C03", list: "company-options" },
  room_id: { label: "Room", placeholder: "Type or pick R04", list: "room-options" },
  delay_minutes: { label: "Delay minutes", placeholder: "e.g. 90" },
};

const KIND_LABELS = {
  student_dropout: "Student dropout",
  panel_dropout: "Panel dropout",
  late_company: "Late company",
  room_unavailable: "Room unavailable",
};

const KIND_SUMMARIES = {
  student_dropout: "Pick a student below to cancel their interview and compact the panel.",
  panel_dropout: "Pick a panel below. All its future interviews get absorbed by surviving panels.",
  late_company: "Pick a company below and specify how late they are.",
  room_unavailable: "Pick a room below. All its future interviews move to another room.",
};

function fmtTime(t) {
  if (!t) return "-";
  return new Date(t).toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function summarizeInterviews(interviews) {
  const summary = {
    Total: interviews.length,
    Scheduled: 0,
    Unscheduled: 0,
    Cancelled: 0,
    OriginalUnscheduled: 0,
    DisruptionUnscheduled: 0,
    UnscheduledReasons: {},
  };

  interviews.forEach((iv) => {
    if (iv.Status === STATUS.Scheduled) {
      summary.Scheduled += 1;
      return;
    }
    if (iv.Status === STATUS.Cancelled) {
      summary.Cancelled += 1;
      return;
    }
    if (iv.Status === STATUS.Unscheduled) {
      summary.Unscheduled += 1;
      const reason = iv.UnscheduledReason || "unspecified";
      summary.UnscheduledReasons[reason] =
        (summary.UnscheduledReasons[reason] || 0) + 1;
      if (isOriginalCapacityGap(reason)) {
        summary.OriginalUnscheduled += 1;
      } else {
        summary.DisruptionUnscheduled += 1;
      }
    }
  });

  return summary;
}

function normalizeBackendSummary(summary) {
  if (!summary) return null;
  const normalized = {
    Total: summary.Total || 0,
    Scheduled: summary.Scheduled || 0,
    Unscheduled: summary.Unscheduled || 0,
    Cancelled: summary.Cancelled || 0,
    OriginalUnscheduled: 0,
    DisruptionUnscheduled: 0,
    UnscheduledReasons: summary.UnscheduledReasons || {},
  };

  Object.entries(normalized.UnscheduledReasons).forEach(([reason, count]) => {
    if (isOriginalCapacityGap(reason)) {
      normalized.OriginalUnscheduled += count;
    } else {
      normalized.DisruptionUnscheduled += count;
    }
  });

  return normalized;
}

function isOriginalCapacityGap(reason) {
  return ORIGINAL_UNSCHEDULED_REASONS.has(reason || "");
}

function unscheduledCategory(iv) {
  if (iv.Status !== STATUS.Unscheduled) return "none";
  return isOriginalCapacityGap(iv.UnscheduledReason)
    ? "original_gap"
    : "disruption_gap";
}

function sortedReasonRows(summary) {
  return Object.entries(summary?.UnscheduledReasons || {}).sort((a, b) => {
    if (b[1] !== a[1]) return b[1] - a[1];
    return a[0].localeCompare(b[0]);
  });
}

function signedDelta(value) {
  if (value > 0) return `+${value}`;
  return String(value);
}

function uniqueSorted(values) {
  return [...new Set(values.filter(Boolean))].sort((a, b) => a.localeCompare(b));
}

function slotEmpty(slot) {
  return !slot || (!slot.PanelID && !slot.RoomID && !slot.At);
}

function buildDiffSummary(diff, kind) {
  if (!diff) return "Run a disruption to see a scoped replan summary.";

  const changes = diff.Changes || [];
  const interviewChanges = changes.filter((c) => c.StudentID);
  const cancelled = interviewChanges.filter((c) => !slotEmpty(c.Before) && slotEmpty(c.After)).length;
  const shifted = interviewChanges.filter(
    (c) => !slotEmpty(c.Before) && !slotEmpty(c.After) && c.Before?.At !== c.After?.At
  ).length;
  const movedPanel = interviewChanges.filter(
    (c) => !slotEmpty(c.Before) && !slotEmpty(c.After) && c.Before?.PanelID !== c.After?.PanelID
  ).length;
  const movedRoom = interviewChanges.filter(
    (c) => !slotEmpty(c.Before) && !slotEmpty(c.After) && c.Before?.RoomID !== c.After?.RoomID
  ).length;
  const pieces = [];

  if (cancelled) pieces.push(`${cancelled} interview${cancelled === 1 ? "" : "s"} cancelled`);
  if (shifted) pieces.push(`${shifted} shifted in time`);
  if (movedPanel) pieces.push(`${movedPanel} moved to another panel`);
  if (movedRoom) pieces.push(`${movedRoom} changed room`);
  if (pieces.length === 0) pieces.push("no interview rows moved");

  return `${KIND_LABELS[kind] || "Disruption"}: ${pieces.join(", ")}.`;
}

export default function App() {
  const [interviews, setInterviews] = useState([]);
  const [baselineSummary, setBaselineSummary] = useState(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);

  const [kind, setKind] = useState("student_dropout");
  const [fields, setFields] = useState({});
  const [at, setAt] = useState("");

  const [diff, setDiff] = useState(null);
  const [comparison, setComparison] = useState(null);
  const [activity, setActivity] = useState([]);
  const [undoLeft, setUndoLeft] = useState(0);
  const [operatingDayEnd, setOperatingDayEnd] = useState("19:00");
  const [submitting, setSubmitting] = useState(false);
  const [undoing, setUndoing] = useState(false);
  const [submitError, setSubmitError] = useState(null);

  const [filter, setFilter] = useState("");
  const [categoryFilter, setCategoryFilter] = useState("all");
  const [page, setPage] = useState(1);

  async function fetchSchedule() {
    const res = await fetch(`${API}/schedule`);
    if (!res.ok) throw new Error(`GET /schedule -> ${res.status}`);
    return (await res.json()) || [];
  }

  async function fetchStatus() {
    const res = await fetch(`${API}/status`);
    if (!res.ok) return null;
    const data = await res.json();
    return {
      summary: normalizeBackendSummary(data.Summary),
      undoLeft: data.UndoLeft || 0,
      operatingDayEnd: data.OperatingDayEnd || "19:00",
    };
  }

  async function loadSchedule({ captureBaseline = false } = {}) {
    setLoading(true);
    setError(null);
    try {
      const [schedule, status] = await Promise.all([fetchSchedule(), fetchStatus()]);
      setInterviews(schedule);
      setUndoLeft(status?.undoLeft || 0);
      setOperatingDayEnd(status?.operatingDayEnd || "19:00");
      if (captureBaseline) {
        setBaselineSummary(status?.summary || summarizeInterviews(schedule));
      }
    } catch (e) {
      setError(e.message);
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    let cancelled = false;

    async function loadInitialSchedule() {
      try {
        const [schedule, status] = await Promise.all([fetchSchedule(), fetchStatus()]);
        if (cancelled) return;
        setInterviews(schedule);
        setUndoLeft(status?.undoLeft || 0);
        setOperatingDayEnd(status?.operatingDayEnd || "19:00");
        setBaselineSummary(status?.summary || summarizeInterviews(schedule));
      } catch (e) {
        if (!cancelled) setError(e.message);
      } finally {
        if (!cancelled) setLoading(false);
      }
    }

    loadInitialSchedule();
    return () => {
      cancelled = true;
    };
  }, []);

  function handleFieldChange(name, value) {
    setFields((f) => ({ ...f, [name]: value }));
  }

  async function submitDisruption(e) {
    e.preventDefault();
    setSubmitting(true);
    setSubmitError(null);
    setDiff(null);

    const optimisticBefore = summarizeInterviews(interviews);
    const atIST = at ? at + ":00+05:30" : new Date().toISOString();
    const body = {
      kind,
      at: atIST,
      ...fields,
    };
    if (body.delay_minutes) {
      body.delay_minutes = Number(body.delay_minutes);
    }

    try {
      const res = await fetch(`${API}/disruptions`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      const text = await res.text();
      if (!res.ok) throw new Error(text || `POST /disruptions -> ${res.status}`);
      const parsed = JSON.parse(text);
      const before = normalizeBackendSummary(parsed.BeforeSummary) || optimisticBefore;
      const after = normalizeBackendSummary(parsed.AfterSummary) || before;

      setDiff(parsed);
      setComparison({ kind, before, after, body });
      setActivity((items) => [
        {
          id: `${Date.now()}-${items.length}`,
          title: KIND_LABELS[kind],
          text: buildDiffSummary(parsed, kind),
          count: (parsed.Changes || []).filter((c) => c.StudentID).length,
        },
        ...items,
      ].slice(0, 6));
      await loadSchedule();
    } catch (e2) {
      setSubmitError(e2.message);
    } finally {
      setSubmitting(false);
    }
  }

  async function undoLastDisruption() {
    setUndoing(true);
    setSubmitError(null);
    try {
      const res = await fetch(`${API}/undo`, { method: "POST" });
      const text = await res.text();
      if (!res.ok) throw new Error(text || `POST /undo -> ${res.status}`);
      const parsed = JSON.parse(text);
      setUndoLeft(parsed.UndoLeft || 0);
      setDiff(null);
      setComparison(null);
      setActivity((items) => [
        {
          id: `${Date.now()}-undo`,
          title: "Undo",
          text: "Rolled back to the exact schedule snapshot from before the last disruption.",
          count: 0,
        },
        ...items,
      ].slice(0, 6));
      await loadSchedule();
    } catch (e) {
      setSubmitError(e.message);
    } finally {
      setUndoing(false);
    }
  }

  const currentSummary = useMemo(
    () => summarizeInterviews(interviews),
    [interviews]
  );

  const optionLists = useMemo(() => {
    const scheduled = interviews.filter((iv) => iv.Status === STATUS.Scheduled);
    const studentContext = {};
    scheduled.forEach((iv) => {
      if (!studentContext[iv.StudentID]) studentContext[iv.StudentID] = iv;
    });
    const panelContext = {};
    scheduled.forEach((iv) => {
      if (!panelContext[iv.PanelID]) panelContext[iv.PanelID] = { company: iv.CompanyID, count: 0 };
      panelContext[iv.PanelID].count++;
    });
    const roomContext = {};
    scheduled.forEach((iv) => {
      if (!roomContext[iv.RoomID]) roomContext[iv.RoomID] = { count: 0 };
      roomContext[iv.RoomID].count++;
    });
    return {
      students: uniqueSorted(scheduled.map((iv) => iv.StudentID)).map((id) => {
        const iv = studentContext[id];
        return iv ? `${id} (${iv.CompanyID})` : id;
      }),
      panels: uniqueSorted(scheduled.map((iv) => iv.PanelID)).map((id) => {
        const info = panelContext[id];
        return info ? `${id} (${info.company}, ${info.count}iv)` : id;
      }),
      companies: uniqueSorted(interviews.map((iv) => iv.CompanyID)),
      rooms: uniqueSorted(scheduled.map((iv) => iv.RoomID)).map((id) => {
        const info = roomContext[id];
        return info ? `${id} (${info.count}iv)` : id;
      }),
    };
  }, [interviews]);

  const filtered = useMemo(() => {
    const needle = filter.trim().toLowerCase();
    return interviews.filter((iv) => {
      if (categoryFilter === "scheduled" && iv.Status !== STATUS.Scheduled) return false;
      if (categoryFilter === "original_gap" && unscheduledCategory(iv) !== "original_gap") return false;
      if (categoryFilter === "disruption_gap" && unscheduledCategory(iv) !== "disruption_gap") return false;
      if (!needle) return true;
      return [iv.StudentID, iv.CompanyID, iv.PanelID, iv.RoomID, iv.UnscheduledReason]
        .filter(Boolean)
        .some((value) => value.toLowerCase().includes(needle));
    });
  }, [interviews, filter, categoryFilter]);

  const pageCount = Math.max(1, Math.ceil(filtered.length / PAGE_SIZE));
  const safePage = Math.min(page, pageCount);
  const pagedRows = filtered.slice((safePage - 1) * PAGE_SIZE, safePage * PAGE_SIZE);
  const activeComparison = comparison || {
    kind: null,
    before: baselineSummary || currentSummary,
    after: currentSummary,
  };
  const affectedInterviewCount = (diff?.Changes || []).filter((change) => change.StudentID).length;
  const coordinationChangeCount = (diff?.Changes?.length || 0) - affectedInterviewCount;

  return (
    <div style={styles.page}>
      <datalist id="student-options">
        {optionLists.students.map((id) => <option value={id} key={id} />)}
      </datalist>
      <datalist id="panel-options">
        {optionLists.panels.map((id) => <option value={id} key={id} />)}
      </datalist>
      <datalist id="company-options">
        {optionLists.companies.map((id) => <option value={id} key={id} />)}
      </datalist>
      <datalist id="room-options">
        {optionLists.rooms.map((id) => <option value={id} key={id} />)}
      </datalist>

      <header style={styles.hero}>
        <div style={styles.brandLockup}>
          <div style={styles.brandMark}>PS</div>
          <div>
            <div style={styles.eyebrow}>Live replanning console</div>
            <h1 style={styles.h1}>PlacementOps Control</h1>
            <p style={styles.subhead}>
              Defend the placement week with scoped replans, audit-ready diffs, and stable operating cutoffs.
            </p>
          </div>
        </div>
        <div style={styles.heroActions}>
          <button onClick={() => loadSchedule()} style={styles.secondaryButton}>
            Refresh
          </button>
          <button
            onClick={undoLastDisruption}
            disabled={undoing || undoLeft === 0}
            style={{
              ...styles.undoButton,
              opacity: undoing || undoLeft === 0 ? 0.5 : 1,
              cursor: undoing || undoLeft === 0 ? "not-allowed" : "pointer",
            }}
          >
            {undoing ? "Undoing..." : `Undo last (${undoLeft})`}
          </button>
        </div>
      </header>

      <div style={styles.shell}>
        <aside style={styles.navRail}>
          <a style={styles.navItem} href="#overview">Overview</a>
          <a style={styles.navItem} href="#disruptions">Act</a>
          <a style={styles.navItem} href="#browser">Inspect</a>
        </aside>

      <main style={styles.main}>
        <section id="overview" style={styles.section}>
          <div style={styles.sectionHeader}>
            <div>
              <span style={styles.moduleLabel}>01 / Overview</span>
              <h2 style={styles.h2}>Schedule Overview</h2>
              <p style={styles.muted}>Current backend state, with unscheduled categories split by origin.</p>
            </div>
          </div>
          <div style={styles.statGrid}>
            <Metric label="Total records" value={currentSummary.Total} tone="neutral" />
            <Metric label="Scheduled" value={currentSummary.Scheduled} tone="good" />
            <Metric label="Original capacity gaps" value={currentSummary.OriginalUnscheduled} tone="warn" />
            <Metric label="Disruption-created gaps" value={currentSummary.DisruptionUnscheduled} tone="bad" />
          </div>
          <div style={styles.baselineStrip}>
            <span style={styles.pillNeutral}>Page-load baseline</span>
            <strong>{baselineSummary?.Scheduled || 0}</strong> scheduled
            <span style={styles.dotSep}>/</span>
            <strong>{baselineSummary?.Unscheduled || 0}</strong> unscheduled
          </div>
        </section>

        <section id="disruptions" style={styles.twoColumn}>
          <div style={styles.section}>
            <div style={styles.sectionHeader}>
              <div>
                <span style={styles.moduleLabel}>02 / Act</span>
                <h2 style={styles.h2}>Disruption Control</h2>
                <p style={styles.muted}>{KIND_SUMMARIES[kind]}</p>
                <div style={styles.cutoffNote}>
                  Same-day cutoff: <strong>{operatingDayEnd}</strong>. Interviews that cannot fit by then stay unscheduled.
                </div>
              </div>
            </div>

            <form onSubmit={submitDisruption} style={styles.form}>
              <label style={styles.label}>
                Disruption type
                <select
                  value={kind}
                  onChange={(e) => {
                    setKind(e.target.value);
                    setFields({});
                    setAt("");
                  }}
                  style={styles.input}
                >
                  <option value="student_dropout">Student dropout</option>
                  <option value="panel_dropout">Panel dropout</option>
                  <option value="late_company">Late company</option>
                  <option value="room_unavailable">Room unavailable</option>
                </select>
              </label>

              <QuickPick interviews={interviews} kind={kind} onSelect={(data) => {
                setFields((f) => ({ ...f, ...data }));
                if (kind === "student_dropout" && data.student_id) {
                  const iv = interviews.find(
                    (i) => i.StudentID === data.student_id && i.Status === STATUS.Scheduled
                  );
                  if (iv) {
                    const d = new Date(iv.StartTime);
                    const pad = (n) => String(n).padStart(2, "0");
                    setAt(`${d.getFullYear()}-${pad(d.getMonth()+1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`);
                  }
                }
              }} />

              {KIND_FIELDS[kind].map((name) => {
                const meta = FIELD_META[name];
                return (
                  <label style={styles.label} key={name}>
                    {meta.label}
                    <input
                      type={name === "delay_minutes" ? "number" : "text"}
                      min={name === "delay_minutes" ? "1" : undefined}
                      list={meta.list}
                      style={styles.input}
                      value={fields[name] || ""}
                      onChange={(e) => handleFieldChange(name, e.target.value)}
                      placeholder={meta.placeholder}
                      required
                    />
                  </label>
                );
              })}

              {(kind === "student_dropout" || kind === "late_company") && (
                <label style={styles.label}>
                  Effective at
                  <input
                    type="datetime-local"
                    style={styles.input}
                    value={at}
                    onChange={(e) => setAt(e.target.value)}
                    required
                  />
                </label>
              )}

              {(kind === "panel_dropout" || kind === "room_unavailable") && (
                <label style={styles.label}>
                  Affect from
                  <select
                    style={styles.input}
                    value={at}
                    onChange={(e) => setAt(e.target.value)}
                    required
                  >
                    {DAY_OPTIONS.map((opt) => (
                      <option key={opt.value} value={opt.value}>{opt.label}</option>
                    ))}
                  </select>
                </label>
              )}

              <button type="submit" disabled={submitting} style={styles.primaryButton}>
                {submitting ? "Applying..." : "Apply disruption"}
              </button>
            </form>

            {submitError && (
              <div style={styles.errorBox}>
                <strong>Error:</strong> {submitError}
              </div>
            )}
          </div>

          <div style={styles.section}>
            <div style={styles.sectionHeader}>
              <div>
                <span style={styles.moduleLabel}>03 / Review</span>
                <h2 style={styles.h2}>Activity / Diff Log</h2>
                <p style={styles.muted}>
                  The diff below is the full blast radius reported by the replan call.
                  Replans do not spill into tomorrow; the active day closes at {operatingDayEnd}.
                </p>
              </div>
            </div>

            <div style={styles.deltaGrid}>
              <DeltaMetric label="Scheduled" before={activeComparison.before?.Scheduled || 0} after={activeComparison.after?.Scheduled || 0} tone="good" />
              <DeltaMetric label="Original gaps" before={activeComparison.before?.OriginalUnscheduled || 0} after={activeComparison.after?.OriginalUnscheduled || 0} tone="warn" />
              <DeltaMetric label="Disruption gaps" before={activeComparison.before?.DisruptionUnscheduled || 0} after={activeComparison.after?.DisruptionUnscheduled || 0} tone="bad" />
            </div>

            <ReasonBreakdown
              before={activeComparison.before}
              after={activeComparison.after}
              showDelta={Boolean(comparison)}
            />

            {diff ? (
              <div style={styles.diffPanel}>
                <div style={styles.diffSummary}>
                  <strong>{buildDiffSummary(diff, comparison?.kind)}</strong>
                  <span>
                    This disruption affected exactly {affectedInterviewCount} interview
                    {affectedInterviewCount === 1 ? "" : "s"}.
                    {coordinationChangeCount > 0
                      ? ` ${coordinationChangeCount} coordination notice${coordinationChangeCount === 1 ? "" : "s"} included.`
                      : ""}
                  </span>
                </div>
                <div style={styles.diffList}>
                  {(diff.Changes || []).map((change, index) => (
                    <DiffCard change={change} index={index} key={`${change.StudentID}-${index}`} />
                  ))}
                </div>
              </div>
            ) : (
              <div style={styles.emptyState}>
                Apply a disruption to see before/after slots, notification targets, and exact scope.
              </div>
            )}

            {activity.length > 0 && (
              <div style={styles.activityList}>
                {activity.map((item) => (
                  <div key={item.id} style={styles.activityItem}>
                    <span style={styles.activityCount}>{item.count}</span>
                    <div>
                      <strong>{item.title}</strong>
                      <p style={styles.mutedTight}>{item.text}</p>
                    </div>
                  </div>
                ))}
              </div>
            )}
          </div>
        </section>

        <section id="browser" style={styles.section}>
          <div style={styles.sectionHeader}>
            <div>
              <span style={styles.moduleLabel}>04 / Inspect</span>
              <h2 style={styles.h2}>Schedule Browser</h2>
              <p style={styles.muted}>
                Paginated view of the current schedule. Filter by student, company, panel, room, or reason.
              </p>
            </div>
            <div style={styles.browserTools}>
              <input
                style={{ ...styles.input, width: "min(250px, 100%)" }}
                placeholder="Search IDs or reasons..."
                value={filter}
                onChange={(e) => {
                  setFilter(e.target.value);
                  setPage(1);
                }}
              />
              <select
                value={categoryFilter}
                onChange={(e) => {
                  setCategoryFilter(e.target.value);
                  setPage(1);
                }}
                style={styles.input}
              >
                <option value="all">All records</option>
                <option value="scheduled">Scheduled only</option>
                <option value="original_gap">Original capacity gaps</option>
                <option value="disruption_gap">Disruption-created gaps</option>
              </select>
            </div>
          </div>

          {loading && <div style={styles.emptyState}>Loading schedule...</div>}
          {error && (
            <div style={styles.errorBox}>
              <strong>Could not load schedule:</strong> {error}
              <div style={{ marginTop: 6, fontSize: 13, opacity: 0.8 }}>
                Start the backend with <code>go run ./cmd/server</code>.
              </div>
            </div>
          )}

          {!loading && !error && (
            <>
              <div style={styles.paginationBar}>
                <span>
                  Showing {filtered.length === 0 ? 0 : (safePage - 1) * PAGE_SIZE + 1}
                  -{Math.min(safePage * PAGE_SIZE, filtered.length)} of {filtered.length}
                </span>
                <div style={styles.pageButtons}>
                  <button
                    onClick={() => setPage((p) => Math.max(1, p - 1))}
                    disabled={safePage === 1}
                    style={styles.pageButton}
                  >
                    Prev
                  </button>
                  <strong>Page {safePage} / {pageCount}</strong>
                  <button
                    onClick={() => setPage((p) => Math.min(pageCount, p + 1))}
                    disabled={safePage === pageCount}
                    style={styles.pageButton}
                  >
                    Next
                  </button>
                </div>
              </div>

              <div style={styles.tableWrap}>
                <table style={styles.table}>
                  <thead>
                    <tr>
                      <th style={styles.th}>Student</th>
                      <th style={styles.th}>Company</th>
                      <th style={styles.th}>Panel</th>
                      <th style={styles.th}>Room</th>
                      <th style={styles.th}>Start</th>
                      <th style={styles.th}>Status</th>
                      <th style={styles.th}>Reason</th>
                    </tr>
                  </thead>
                  <tbody>
                    {pagedRows.map((iv) => (
                      <tr key={iv.ID}>
                        <td style={styles.tdStrong}>{iv.StudentID || "-"}</td>
                        <td style={styles.td}>{iv.CompanyID || "-"}</td>
                        <td style={styles.td}>{iv.PanelID || "-"}</td>
                        <td style={styles.td}>{iv.RoomID || "-"}</td>
                        <td style={styles.td}>{fmtTime(iv.StartTime)}</td>
                        <td style={styles.td}>
                          <StatusBadge interview={iv} />
                        </td>
                        <td style={styles.reasonCell}>{iv.UnscheduledReason || "-"}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
                {pagedRows.length === 0 && (
                  <div style={styles.emptyState}>No records match the current filters.</div>
                )}
              </div>
            </>
          )}
        </section>
      </main>
      </div>
    </div>
  );
}

function Metric({ label, value, tone }) {
  return (
    <div style={{ ...styles.metric, borderLeftColor: toneColor(tone) }}>
      <div style={{ ...styles.metricValue, color: toneColor(tone) }}>{value}</div>
      <div style={styles.metricLabel}>{label}</div>
    </div>
  );
}

function DeltaMetric({ label, before, after, tone }) {
  const delta = after - before;
  return (
    <div style={styles.deltaMetric}>
      <span style={styles.metricLabel}>{label}</span>
      <div style={styles.deltaFlow}>
        <strong>{before}</strong>
        <span>-&gt;</span>
        <strong>{after}</strong>
      </div>
      <div style={{ ...styles.deltaText, color: toneColor(tone) }}>
        {signedDelta(delta)} in latest action
      </div>
    </div>
  );
}

function ReasonBreakdown({ before, after, showDelta }) {
  const reasons = new Set([
    ...Object.keys(before?.UnscheduledReasons || {}),
    ...Object.keys(after?.UnscheduledReasons || {}),
  ]);
  const rows = showDelta
    ? [...reasons]
        .map((reason) => ({
          reason,
          before: before?.UnscheduledReasons?.[reason] || 0,
          after: after?.UnscheduledReasons?.[reason] || 0,
        }))
        .sort((a, b) => b.after - a.after || a.reason.localeCompare(b.reason))
    : sortedReasonRows(after || before).map(([reason, count]) => ({
        reason,
        before: count,
        after: count,
      }));

  return (
    <div style={styles.reasonBox}>
      <div style={styles.reasonHeader}>
        <h3 style={styles.h3}>Unscheduled Reasons</h3>
        <span style={styles.smallCaps}>{showDelta ? "Before / after" : "Current snapshot"}</span>
      </div>
      {rows.length === 0 ? (
        <div style={styles.emptyState}>No unscheduled interviews.</div>
      ) : (
        <div style={styles.reasonRows}>
          {rows.map((row) => {
            const original = isOriginalCapacityGap(row.reason);
            const delta = row.after - row.before;
            return (
              <div key={row.reason} style={styles.reasonRow}>
                <span style={original ? styles.reasonDotOriginal : styles.reasonDotDisruption} />
                <span style={styles.reasonText}>{row.reason}</span>
                <span style={original ? styles.reasonLabelOriginal : styles.reasonLabelDisruption}>
                  {original ? "Original gap" : "Disruption-created"}
                </span>
                <strong>
                  {showDelta
                    ? `${row.before} -> ${row.after} (${signedDelta(delta)})`
                    : row.after}
                </strong>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

function DiffCard({ change, index }) {
  const notify = change.Notify || [];
  return (
    <div style={styles.diffCard}>
      <div style={styles.diffTopLine}>
        <div>
          <span style={styles.smallCaps}>Change {index + 1}</span>
          <h3 style={styles.diffTitle}>{change.StudentID || "Coordination event"}</h3>
        </div>
        <span style={slotEmpty(change.After) ? styles.outcomeCancelled : styles.outcomeMoved}>
          {slotEmpty(change.After) ? "Unscheduled" : "Rescheduled"}
        </span>
      </div>
      <div style={styles.slotCompare}>
        <SlotBlock title="Before" slot={change.Before} />
        <div style={styles.compareArrow}>-&gt;</div>
        <SlotBlock title="After" slot={change.After} />
      </div>
      {notify.length > 0 && (
        <div style={styles.notifyRow}>
          {notify.map((target, i) => (
            <NotifyChip target={target} key={`${target.Kind}-${target.ID}-${i}`} />
          ))}
        </div>
      )}
    </div>
  );
}

function SlotBlock({ title, slot }) {
  const empty = slotEmpty(slot);
  return (
    <div style={empty ? styles.slotBlockEmpty : styles.slotBlock}>
      <span style={styles.slotTitle}>{title}</span>
      {empty ? (
        <strong>Unscheduled</strong>
      ) : (
        <>
          <strong>{slot.PanelID || "-"} / {slot.RoomID || "-"}</strong>
          <span>{fmtTime(slot.At)}</span>
        </>
      )}
    </div>
  );
}

function NotifyChip({ target }) {
  const stylesByKind = {
    student: styles.notifyStudent,
    panel: styles.notifyPanel,
    coordinator: styles.notifyCoordinator,
  };
  const marker = {
    student: "STU",
    panel: "PAN",
    coordinator: "CO",
  };
  return (
    <span style={{ ...styles.notifyChip, ...(stylesByKind[target.Kind] || styles.notifyCoordinator) }}>
      <strong>{marker[target.Kind] || "MSG"}</strong>
      {target.ID ? ` ${target.ID}: ` : " "}
      {target.Message}
    </span>
  );
}

function QuickPick({ interviews, kind, onSelect }) {
  const scheduled = interviews.filter((iv) => iv.Status === STATUS.Scheduled);

  let items = [];
  if (kind === "student_dropout") {
    const byStudent = {};
    scheduled.forEach((iv) => {
      if (!byStudent[iv.StudentID]) byStudent[iv.StudentID] = [];
      byStudent[iv.StudentID].push(iv);
    });
    items = Object.entries(byStudent)
      .map(([sid, ivs]) => {
        const iv = ivs[0];
        return {
          id: sid,
          main: sid,
          sub: `${iv.CompanyID} | ${iv.PanelID} | ${iv.RoomID}`,
          time: fmtTime(iv.StartTime),
          data: { student_id: sid },
        };
      })
      .sort((a, b) => a.main.localeCompare(b.main));
  } else if (kind === "panel_dropout") {
    const byPanel = {};
    scheduled.forEach((iv) => {
      if (!byPanel[iv.PanelID]) byPanel[iv.PanelID] = { company: iv.CompanyID, count: 0, rooms: new Set() };
      byPanel[iv.PanelID].count++;
      byPanel[iv.PanelID].rooms.add(iv.RoomID);
    });
    items = Object.entries(byPanel)
      .map(([pid, info]) => ({
        id: pid,
        main: pid,
        sub: `${info.company} | ${info.count} interviews | Room ${[...info.rooms].join(",")}`,
        time: "",
        data: { panel_id: pid },
      }))
      .sort((a, b) => a.main.localeCompare(b.main));
  } else if (kind === "room_unavailable") {
    const byRoom = {};
    scheduled.forEach((iv) => {
      if (!byRoom[iv.RoomID]) byRoom[iv.RoomID] = { panels: new Set(), count: 0 };
      byRoom[iv.RoomID].panels.add(iv.PanelID);
      byRoom[iv.RoomID].count++;
    });
    items = Object.entries(byRoom)
      .map(([rid, info]) => ({
        id: rid,
        main: rid,
        sub: `${info.count} interviews | Panels: ${[...info.panels].slice(0, 3).join(",")}${info.panels.size > 3 ? "..." : ""}`,
        time: "",
        data: { room_id: rid },
      }))
      .sort((a, b) => a.main.localeCompare(b.main));
  } else if (kind === "late_company") {
    const byCompany = {};
    scheduled.forEach((iv) => {
      if (!byCompany[iv.CompanyID]) byCompany[iv.CompanyID] = { count: 0, panels: new Set(), earliest: iv.StartTime };
      byCompany[iv.CompanyID].count++;
      byCompany[iv.CompanyID].panels.add(iv.PanelID);
    });
    items = Object.entries(byCompany)
      .map(([cid, info]) => ({
        id: cid,
        main: cid,
        sub: `${info.count} interviews | ${info.panels.size} panels`,
        time: fmtTime(info.earliest),
        data: { company_id: cid },
      }))
      .sort((a, b) => a.main.localeCompare(b.main));
  }

  if (items.length === 0) return null;

  return (
    <div style={styles.quickPick}>
      <div style={styles.quickPickHeader}>Click to select:</div>
      <div style={styles.quickPickList}>
        {items.map((item) => (
          <button
            type="button"
            key={item.id}
            onClick={() => onSelect(item.data)}
            style={styles.quickPickItem}
          >
            <span style={styles.quickPickMain}>{item.main}</span>
            <span style={styles.quickPickSub}>{item.sub}</span>
            {item.time && <span style={styles.quickPickTime}>{item.time}</span>}
          </button>
        ))}
      </div>
    </div>
  );
}

function StatusBadge({ interview }) {
  if (interview.Status === STATUS.Scheduled) {
    return <span style={styles.badgeScheduled}>Scheduled</span>;
  }
  if (interview.Status === STATUS.Cancelled) {
    return <span style={styles.badgeCancelled}>Cancelled</span>;
  }
  if (unscheduledCategory(interview) === "original_gap") {
    return <span style={styles.badgeOriginal}>Original gap</span>;
  }
  return <span style={styles.badgeDisruption}>Disruption gap</span>;
}

function toneColor(tone) {
  return {
    good: "#10b981",
    warn: "#f59e0b",
    bad: "#ef4444",
    neutral: "#6366f1",
  }[tone] || "#6366f1";
}

const styles = {
  page: {
    minHeight: "100vh",
    boxSizing: "border-box",
    padding: 0,
    background: "#f7f7f4",
    color: "#171717",
    fontFamily: "'Inter', system-ui, -apple-system, 'Segoe UI', sans-serif",
    textAlign: "left",
  },
  hero: {
    display: "flex",
    justifyContent: "space-between",
    alignItems: "center",
    flexWrap: "wrap",
    gap: 24,
    padding: "22px 32px",
    background: "#101010",
    borderBottom: "1px solid #242424",
    marginBottom: 0,
  },
  brandLockup: {
    display: "flex",
    alignItems: "center",
    gap: 14,
    minWidth: 0,
  },
  brandMark: {
    display: "inline-flex",
    alignItems: "center",
    justifyContent: "center",
    width: 42,
    height: 42,
    borderRadius: 8,
    background: "#f5f5f0",
    color: "#111111",
    fontSize: 13,
    fontWeight: 900,
  },
  eyebrow: {
    color: "#a3a3a3",
    fontSize: 11,
    fontWeight: 700,
    textTransform: "uppercase",
    letterSpacing: 0,
    marginBottom: 6,
  },
  h1: { margin: 0, fontSize: 27, lineHeight: 1.08, fontWeight: 800, color: "#ffffff" },
  h2: { margin: "5px 0 0", fontSize: 18, fontWeight: 750, color: "#171717" },
  h3: { margin: 0, fontSize: 13, fontWeight: 700, color: "#404040" },
  subhead: { margin: "6px 0 0", color: "#d4d4d4", fontSize: 13, lineHeight: 1.45, maxWidth: 680 },
  muted: { margin: "5px 0 0", color: "#6b6b64", fontSize: 12, lineHeight: 1.45 },
  mutedTight: { margin: "2px 0 0", color: "#6b6b64", fontSize: 11, lineHeight: 1.35 },
  cutoffNote: {
    display: "inline-flex",
    gap: 4,
    marginTop: 8,
    padding: "5px 10px",
    borderRadius: 8,
    background: "#fff7d6",
    color: "#6f4e00",
    fontSize: 11,
    fontWeight: 600,
    border: "1px solid #ecd47a",
  },
  heroActions: { display: "flex", gap: 8, flexWrap: "wrap", justifyContent: "flex-end" },
  shell: {
    display: "grid",
    gridTemplateColumns: "minmax(0, 1fr)",
    gap: 18,
    padding: "26px 32px 44px",
    maxWidth: 1480,
    margin: "0 auto",
  },
  navRail: {
    display: "flex",
    flexDirection: "row",
    flexWrap: "wrap",
    gap: 6,
    alignSelf: "start",
    padding: 8,
    border: "1px solid #deded7",
    borderRadius: 8,
    background: "#ffffff",
  },
  navItem: {
    color: "#52524c",
    textDecoration: "none",
    fontSize: 12,
    fontWeight: 700,
    borderRadius: 6,
    padding: "9px 10px",
  },
  main: { display: "flex", flexDirection: "column", gap: 22, minWidth: 0 },
  section: {
    background: "#ffffff",
    border: "1px solid #deded7",
    borderRadius: 8,
    padding: 24,
    boxShadow: "0 1px 2px rgba(16,16,16,0.04)",
  },
  sectionHeader: {
    display: "flex",
    justifyContent: "space-between",
    flexWrap: "wrap",
    gap: 12,
    alignItems: "flex-start",
    marginBottom: 18,
  },
  moduleLabel: {
    color: "#8a8a82",
    fontSize: 11,
    fontWeight: 800,
    textTransform: "uppercase",
    letterSpacing: 0,
  },
  twoColumn: {
    display: "grid",
    gridTemplateColumns: "repeat(auto-fit, minmax(min(100%, 420px), 1fr))",
    gap: 22,
    alignItems: "start",
  },
  statGrid: {
    display: "grid",
    gridTemplateColumns: "repeat(auto-fit, minmax(180px, 1fr))",
    gap: 14,
  },
  metric: {
    border: "1px solid #e6e6df",
    borderLeft: "4px solid",
    borderTop: "1px solid #e6e6df",
    borderRadius: 8,
    padding: "16px 18px",
    background: "#fbfbf8",
    transition: "transform 0.15s ease, box-shadow 0.15s ease",
  },
  metricValue: { fontSize: 29, fontWeight: 850, lineHeight: 1, letterSpacing: 0 },
  metricLabel: { marginTop: 7, fontSize: 10, color: "#6b6b64", textTransform: "uppercase", fontWeight: 700, letterSpacing: 0 },
  baselineStrip: {
    display: "flex",
    alignItems: "center",
    gap: 8,
    marginTop: 14,
    padding: "11px 14px",
    borderRadius: 8,
    background: "#f2f2ed",
    border: "1px solid #dfdfd7",
    color: "#52524c",
    fontSize: 12,
    flexWrap: "wrap",
  },
  pillNeutral: {
    background: "#171717",
    color: "#ffffff",
    borderRadius: 6,
    padding: "3px 10px",
    fontSize: 10,
    fontWeight: 700,
    textTransform: "uppercase",
    letterSpacing: 0,
  },
  dotSep: { color: "#9c9c94" },
  form: { display: "flex", flexDirection: "column", gap: 13 },
  label: {
    display: "flex",
    flexDirection: "column",
    gap: 5,
    color: "#40403a",
    fontSize: 12,
    fontWeight: 700,
  },
  input: {
    border: "1px solid #d9d9d2",
    borderRadius: 8,
    padding: "10px 12px",
    fontSize: 13,
    background: "#fbfbf8",
    color: "#171717",
    boxSizing: "border-box",
    transition: "border-color 0.15s ease, box-shadow 0.15s ease",
    outline: "none",
  },
  primaryButton: {
    border: "none",
    borderRadius: 8,
    padding: "11px 16px",
    background: "#171717",
    color: "#ffffff",
    fontWeight: 700,
    fontSize: 13,
    cursor: "pointer",
    boxShadow: "0 1px 1px rgba(16,16,16,0.2)",
    transition: "transform 0.1s ease, box-shadow 0.1s ease",
  },
  secondaryButton: {
    border: "1px solid #3a3a3a",
    borderRadius: 8,
    padding: "9px 14px",
    background: "#181818",
    color: "#f4f4f0",
    fontWeight: 600,
    fontSize: 13,
    cursor: "pointer",
    transition: "background 0.15s ease",
  },
  undoButton: {
    border: "1px solid #5d5135",
    borderRadius: 8,
    padding: "9px 14px",
    background: "#221f16",
    color: "#ffd979",
    fontWeight: 700,
    fontSize: 13,
    transition: "background 0.15s ease",
  },
  errorBox: {
    marginTop: 12,
    background: "#fff1f1",
    color: "#b91c1c",
    padding: 12,
    borderRadius: 8,
    fontSize: 12,
    border: "1px solid #f4b4b4",
  },
  deltaGrid: {
    display: "grid",
    gridTemplateColumns: "repeat(auto-fit, minmax(160px, 1fr))",
    gap: 10,
  },
  deltaMetric: {
    border: "1px solid #e6e6df",
    borderRadius: 8,
    padding: 12,
    background: "#fbfbf8",
    minWidth: 0,
  },
  deltaFlow: {
    display: "flex",
    gap: 6,
    alignItems: "baseline",
    marginTop: 6,
    fontSize: 17,
    fontWeight: 700,
  },
  deltaText: { marginTop: 3, fontSize: 11, fontWeight: 700 },
  reasonBox: { marginTop: 18, borderTop: "1px solid #e6e6df", paddingTop: 18 },
  reasonHeader: { display: "flex", justifyContent: "space-between", gap: 10, alignItems: "center" },
  smallCaps: {
    color: "#74746d",
    fontSize: 10,
    letterSpacing: 0,
    textTransform: "uppercase",
    fontWeight: 700,
  },
  reasonRows: { display: "flex", flexDirection: "column", gap: 0, marginTop: 10 },
  reasonRow: {
    display: "grid",
    gridTemplateColumns: "8px minmax(150px, 1fr) auto auto",
    gap: 10,
    alignItems: "center",
    fontSize: 12,
    padding: "10px 0",
    borderBottom: "1px solid #eeeeea",
  },
  reasonDotOriginal: { width: 8, height: 8, borderRadius: 8, background: "#d99400" },
  reasonDotDisruption: { width: 8, height: 8, borderRadius: 8, background: "#dc2626" },
  reasonText: { color: "#40403a", fontWeight: 500 },
  reasonLabelOriginal: {
    background: "#fff7d6",
    color: "#6f4e00",
    borderRadius: 6,
    padding: "3px 8px",
    fontSize: 10,
    fontWeight: 600,
    border: "1px solid #ecd47a",
  },
  reasonLabelDisruption: {
    background: "#fff1f1",
    color: "#b91c1c",
    borderRadius: 6,
    padding: "3px 8px",
    fontSize: 10,
    fontWeight: 600,
    border: "1px solid #f4b4b4",
  },
  diffPanel: { marginTop: 18, borderTop: "1px solid #e6e6df", paddingTop: 18 },
  diffSummary: {
    display: "flex",
    flexDirection: "column",
    gap: 4,
    padding: 14,
    borderRadius: 8,
    background: "#f2f2ed",
    color: "#2f2f2a",
    border: "1px solid #dfdfd7",
    fontSize: 12,
  },
  diffList: { display: "flex", flexDirection: "column", gap: 10, marginTop: 12 },
  diffCard: {
    border: "1px solid #e6e6df",
    borderRadius: 8,
    padding: 14,
    background: "#ffffff",
    transition: "box-shadow 0.15s ease",
  },
  diffTopLine: { display: "flex", justifyContent: "space-between", gap: 10, alignItems: "flex-start" },
  diffTitle: { margin: "3px 0 0", fontSize: 14, fontWeight: 600 },
  outcomeCancelled: {
    background: "#fff1f1",
    color: "#b91c1c",
    borderRadius: 6,
    padding: "3px 10px",
    fontSize: 10,
    fontWeight: 700,
    border: "1px solid #f4b4b4",
  },
  outcomeMoved: {
    background: "#e7f8ef",
    color: "#065f46",
    borderRadius: 6,
    padding: "3px 10px",
    fontSize: "10px",
    fontWeight: 700,
    border: "1px solid #9ed8b7",
  },
  slotCompare: {
    display: "grid",
    gridTemplateColumns: "minmax(0, 1fr) auto minmax(0, 1fr)",
    gap: 10,
    alignItems: "center",
    marginTop: 10,
  },
  slotBlock: {
    display: "flex",
    flexDirection: "column",
    gap: 3,
    border: "1px solid #d9d9d2",
    background: "#fbfbf8",
    borderRadius: 8,
    padding: 10,
    minWidth: 0,
    fontSize: 11,
  },
  slotBlockEmpty: {
    display: "flex",
    flexDirection: "column",
    gap: 3,
    border: "1px solid #f4b4b4",
    background: "#fff7f7",
    borderRadius: 8,
    padding: 10,
    minWidth: 0,
    fontSize: 11,
  },
  slotTitle: { color: "#74746d", fontSize: 9, fontWeight: 700, textTransform: "uppercase", letterSpacing: 0 },
  compareArrow: { color: "#8a8a82", fontWeight: 900, fontSize: 16 },
  notifyRow: { display: "flex", gap: 6, flexWrap: "wrap", marginTop: 10 },
  notifyChip: {
    display: "inline-flex",
    gap: 4,
    alignItems: "center",
    borderRadius: 6,
    padding: "4px 8px",
    fontSize: 10,
    fontWeight: 600,
  },
  notifyStudent: { background: "#eef6ff", color: "#1d4e89", border: "1px solid #bcd7f0" },
  notifyPanel: { background: "#f4f2ff", color: "#51458b", border: "1px solid #d8d1f2" },
  notifyCoordinator: { background: "#fff5e8", color: "#9a5516", border: "1px solid #edc99d" },
  activityList: { display: "flex", flexDirection: "column", gap: 8, marginTop: 16 },
  activityItem: {
    display: "grid",
    gridTemplateColumns: "32px 1fr",
    gap: 10,
    alignItems: "start",
    padding: 10,
    border: "1px solid #e6e6df",
    borderRadius: 8,
    background: "#fbfbf8",
    fontSize: 12,
    transition: "box-shadow 0.15s ease",
  },
  activityCount: {
    display: "inline-flex",
    alignItems: "center",
    justifyContent: "center",
    width: 28,
    height: 28,
    borderRadius: 8,
    background: "#171717",
    color: "#ffffff",
    fontWeight: 800,
    fontSize: 12,
  },
  browserTools: { display: "flex", gap: 8, flexWrap: "wrap", justifyContent: "flex-start", maxWidth: "100%" },
  paginationBar: {
    display: "flex",
    justifyContent: "space-between",
    gap: 10,
    alignItems: "center",
    marginBottom: 12,
    color: "#52524c",
    fontSize: 12,
    flexWrap: "wrap",
  },
  pageButtons: { display: "flex", alignItems: "center", gap: 8 },
  pageButton: {
    border: "1px solid #d9d9d2",
    borderRadius: 8,
    padding: "6px 12px",
    background: "#fbfbf8",
    color: "#40403a",
    fontWeight: 600,
    fontSize: 12,
    cursor: "pointer",
    transition: "all 0.15s ease",
  },
  tableWrap: { border: "1px solid #deded7", borderRadius: 8, overflowX: "auto", boxShadow: "0 1px 2px rgba(16,16,16,0.04)" },
  table: { width: "100%", borderCollapse: "collapse", fontSize: 12 },
  th: {
    textAlign: "left",
    padding: "11px 14px",
    background: "#f2f2ed",
    borderBottom: "1px solid #deded7",
    color: "#52524c",
    fontSize: 11,
    fontWeight: 600,
    textTransform: "uppercase",
    letterSpacing: 0,
  },
  td: { padding: "10px 14px", borderBottom: "1px solid #eeeeea", whiteSpace: "nowrap" },
  tdStrong: {
    padding: "10px 14px",
    borderBottom: "1px solid #eeeeea",
    whiteSpace: "nowrap",
    fontWeight: 700,
    color: "#171717",
  },
  reasonCell: {
    padding: "10px 14px",
    borderBottom: "1px solid #eeeeea",
    color: "#6b6b64",
    minWidth: 210,
    fontSize: 11,
  },
  badgeScheduled: {
    background: "#e7f8ef",
    color: "#065f46",
    borderRadius: 6,
    padding: "3px 10px",
    fontSize: 11,
    fontWeight: 600,
    whiteSpace: "nowrap",
    border: "1px solid #9ed8b7",
  },
  badgeCancelled: {
    background: "#f2f2ed",
    color: "#52524c",
    borderRadius: 6,
    padding: "3px 10px",
    fontSize: 11,
    fontWeight: 600,
    whiteSpace: "nowrap",
    border: "1px solid #d9d9d2",
  },
  badgeOriginal: {
    background: "#fff7d6",
    color: "#6f4e00",
    borderRadius: 6,
    padding: "3px 10px",
    fontSize: 11,
    fontWeight: 600,
    whiteSpace: "nowrap",
    border: "1px solid #ecd47a",
  },
  badgeDisruption: {
    background: "#fff1f1",
    color: "#b91c1c",
    borderRadius: 6,
    padding: "3px 10px",
    fontSize: 11,
    fontWeight: 600,
    whiteSpace: "nowrap",
    border: "1px solid #f4b4b4",
  },
  emptyState: {
    marginTop: 10,
    padding: 14,
    border: "1px dashed #cfcfc7",
    borderRadius: 8,
    background: "#fbfbf8",
    color: "#6b6b64",
    fontSize: 12,
    textAlign: "center",
  },
  quickPick: {
    marginBottom: 14,
    border: "1px solid #deded7",
    borderRadius: 8,
    background: "#ffffff",
    overflow: "hidden",
  },
  quickPickHeader: {
    padding: "8px 12px",
    background: "#f2f2ed",
    fontSize: 11,
    fontWeight: 600,
    color: "#52524c",
    textTransform: "uppercase",
    letterSpacing: 0,
    borderBottom: "1px solid #deded7",
  },
  quickPickList: {
    display: "flex",
    flexDirection: "column",
    maxHeight: 200,
    overflowY: "auto",
  },
  quickPickItem: {
    display: "grid",
    gridTemplateColumns: "auto 1fr auto",
    gap: "4px 10px",
    alignItems: "center",
    padding: "7px 12px",
    border: "none",
    borderBottom: "1px solid #eeeeea",
    background: "transparent",
    cursor: "pointer",
    textAlign: "left",
    fontSize: 12,
    transition: "background 0.1s ease",
  },
  quickPickMain: {
    fontWeight: 700,
    color: "#171717",
    fontFamily: "monospace",
    fontSize: 12,
  },
  quickPickSub: {
    color: "#6b6b64",
    fontSize: 11,
    whiteSpace: "nowrap",
    overflow: "hidden",
    textOverflow: "ellipsis",
  },
  quickPickTime: {
    color: "#1d4e89",
    fontWeight: 600,
    fontSize: 11,
    whiteSpace: "nowrap",
  },
};
