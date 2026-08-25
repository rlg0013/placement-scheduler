import { useEffect, useMemo, useState } from "react";
import { useTheme } from "./ThemeContext.jsx";
import "./App.css";

const API = import.meta.env.VITE_API_URL || (import.meta.env.PROD ? "/api" : "http://localhost:8080");
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
  panel_id: { label: "Panel", placeholder: "Type or pick MASS-01-P1", list: "panel-options" },
  company_id: { label: "Company", placeholder: "Type or pick MASS-01", list: "company-options" },
  room_id: { label: "Room", placeholder: "Type or pick R01", list: "room-options" },
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
  return !slot || (!slot.PanelID && !slot.RoomID);
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

/* ---------- Small presentational components ---------- */

function Metric({ label, value, tone, icon }) {
  return (
    <div className="hover-lift" style={{ ...styles.metric, ...metricAccent(tone) }}>
      <div style={styles.metricTop}>
        <span style={{ ...styles.metricIcon, background: toneGlow(tone) }}>{icon}</span>
        <span style={{ ...styles.metricValue, color: toneColor(tone) }}>{value}</span>
      </div>
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
        <span style={styles.deltaArrow}>→</span>
        <strong style={{ color: toneColor(tone) }}>{after}</strong>
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
                  {original ? "Original gap" : "Disruption"}
                </span>
                <strong>
                  {showDelta
                    ? `${row.before} → ${row.after} (${signedDelta(delta)})`
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
    <div className="hover-lift" style={styles.diffCard}>
      <div style={styles.diffTopLine}>
        <div>
          <span style={styles.smallCaps}>Change {index + 1}</span>
          <h3 style={styles.diffTitle}>{change.StudentID || "Coordination event"}</h3>
        </div>
        <span style={slotEmpty(change.After) ? styles.outcomeCancelled : styles.outcomeMoved}>
          {slotEmpty(change.After) ? "✕ Unscheduled" : "↻ Rescheduled"}
        </span>
      </div>
      <div style={styles.slotCompare}>
        <SlotBlock title="Before" slot={change.Before} />
        <div style={styles.compareArrow}>→</div>
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

function QuickPick({ interviews, kind, onSelect }) {
  const scheduled = interviews.filter((iv) => iv.Status === STATUS.Scheduled);

  let items = [];
  let hint = "";
  if (kind === "student_dropout") {
    hint = "Students with scheduled interviews";
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
          sub: `${iv.CompanyID} · ${iv.PanelID} · ${iv.RoomID}`,
          time: fmtTime(iv.StartTime),
          data: { student_id: sid },
        };
      })
      .sort((a, b) => a.main.localeCompare(b.main));
  } else if (kind === "panel_dropout") {
    hint = "Active panels";
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
        sub: `${info.company} · ${info.count} interviews · Room ${[...info.rooms].join(", ")}`,
        time: "",
        data: { panel_id: pid },
      }))
      .sort((a, b) => a.main.localeCompare(b.main));
  } else if (kind === "room_unavailable") {
    hint = "Rooms in use";
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
        sub: `${info.count} interviews · ${info.panels.size} panels`,
        time: "",
        data: { room_id: rid },
      }))
      .sort((a, b) => a.main.localeCompare(b.main));
  } else if (kind === "late_company") {
    hint = "Companies on-site today";
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
        sub: `${info.count} interviews · ${info.panels.size} panels`,
        time: fmtTime(info.earliest),
        data: { company_id: cid },
      }))
      .sort((a, b) => a.main.localeCompare(b.main));
  }

  if (items.length === 0) return null;

  return (
    <div style={styles.quickPick}>
      <div style={styles.quickPickHeader}>
        <span>{hint}</span>
        <span style={styles.quickPickCount}>{items.length}</span>
      </div>
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
            {item.time ? (
              <span style={styles.quickPickTime}>{item.time}</span>
            ) : (
              <span style={styles.quickPickChevron}>→</span>
            )}
          </button>
        ))}
      </div>
    </div>
  );
}

function ThemeToggle() {
  const { theme, toggleTheme } = useTheme();
  return (
    <button
      onClick={toggleTheme}
      style={styles.themeToggle}
      aria-label={`Switch to ${theme === "light" ? "dark" : "light"} mode`}
      title={`Switch to ${theme === "light" ? "dark" : "light"} mode`}
    >
      <span style={styles.themeToggleIcon}>{theme === "light" ? "☾" : "☀"}</span>
      <span style={styles.themeToggleLabel}>{theme === "light" ? "Dark" : "Light"}</span>
    </button>
  );
}

/* ---------- Tone helpers ---------- */

function toneColor(tone) {
  return {
    good: "var(--success)",
    warn: "var(--warning)",
    bad: "var(--danger)",
    neutral: "var(--primary)",
  }[tone] || "var(--primary)";
}

function toneGlow(tone) {
  return {
    good: "var(--success-glow)",
    warn: "var(--warning-glow)",
    bad: "var(--danger-glow)",
    neutral: "var(--shadow-glow)",
  }[tone] || "var(--shadow-glow)";
}

function metricAccent(tone) {
  return {
    good: { borderTopColor: "var(--success)" },
    warn: { borderTopColor: "var(--warning)" },
    bad: { borderTopColor: "var(--danger)" },
    neutral: { borderTopColor: "var(--primary)" },
  }[tone] || { borderTopColor: "var(--primary)" };
}

/* ---------- Main App ---------- */

export default function App() {
  const { theme } = useTheme();
  const [interviews, setInterviews] = useState([]);
  const [baselineSummary, setBaselineSummary] = useState(null);
  const [liveSummary, setLiveSummary] = useState(null);
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
    const res = await fetch(`${API}/schedule`, { cache: "no-store" });
    if (!res.ok) throw new Error(`GET /schedule -> ${res.status}`);
    return (await res.json()) || [];
  }

  async function fetchStatus() {
    const res = await fetch(`${API}/status`, { cache: "no-store" });
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
      const statusSummary = status?.summary || summarizeInterviews(schedule);
      setInterviews(schedule);
      setLiveSummary(statusSummary);
      setUndoLeft(status?.undoLeft || 0);
      setOperatingDayEnd(status?.operatingDayEnd || "19:00");
      if (captureBaseline) {
        setBaselineSummary(statusSummary);
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
        const statusSummary = status?.summary || summarizeInterviews(schedule);
        setInterviews(schedule);
        setLiveSummary(statusSummary);
        setUndoLeft(status?.undoLeft || 0);
        setOperatingDayEnd(status?.operatingDayEnd || "19:00");
        setBaselineSummary(statusSummary);
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

  function handleQuickSelect(data) {
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
      setLiveSummary(after);
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
      const restoredSummary = normalizeBackendSummary(parsed.Summary);
      if (restoredSummary) {
        setLiveSummary(restoredSummary);
      }
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

  const rowSummary = useMemo(
    () => summarizeInterviews(interviews),
    [interviews]
  );
  const currentSummary = liveSummary || rowSummary;

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
  const scheduleRate = currentSummary.Total > 0
    ? Math.round((currentSummary.Scheduled / currentSummary.Total) * 100)
    : 0;

  return (
    <div style={styles.page} data-theme={theme}>
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

      {/* Ambient background orbs */}
      <div style={styles.orb} className="orb orb-1" />
      <div style={styles.orb} className="orb orb-2" />
      <div style={styles.orb} className="orb orb-3" />

      <header style={styles.hero}>
        <div style={styles.heroContent}>
          <div style={styles.heroBadge}>
            <span style={styles.pulseDot} />
            Live replanning defense console
          </div>
          <h1 style={styles.h1}>PlacementOps Control</h1>
          <p style={styles.subhead}>
            A scheduling cockpit that separates baseline capacity gaps from disruption-driven change —
            800 students · 35 companies · 20 rooms · hard 7&nbsp;PM cutoff.
          </p>
        </div>
        <div style={styles.heroActions}>
          <button onClick={() => loadSchedule()} style={styles.secondaryButton}>
            ⟳ Refresh
          </button>
          <button
            onClick={undoLastDisruption}
            disabled={undoing || undoLeft === 0}
            style={{
              ...styles.undoButton,
              opacity: undoing || undoLeft === 0 ? 0.45 : 1,
              cursor: undoing || undoLeft === 0 ? "not-allowed" : "pointer",
            }}
          >
            ↩ {undoing ? "Undoing…" : `Undo (${undoLeft})`}
          </button>
          <ThemeToggle />
        </div>
      </header>

      <main style={styles.main}>
        {/* ---------- Overview ---------- */}
        <section className="animate-slide-up" style={styles.section}>
          <div style={styles.sectionHeader}>
            <div>
              <h2 style={styles.h2}>Schedule Overview</h2>
              <p style={styles.muted}>Current backend state, with unscheduled categories split by origin.</p>
            </div>
            <div style={styles.rateRing}>
              <svg width="64" height="64" viewBox="0 0 64 64">
                <circle cx="32" cy="32" r="26" fill="none" stroke="var(--border)" strokeWidth="7" />
                <circle
                  cx="32" cy="32" r="26" fill="none"
                  stroke="url(#ringGrad)" strokeWidth="7" strokeLinecap="round"
                  strokeDasharray={`${(scheduleRate / 100) * 163.4} 163.4`}
                  transform="rotate(-90 32 32)"
                />
                <defs>
                  <linearGradient id="ringGrad" x1="0%" y1="0%" x2="100%" y2="100%">
                    <stop offset="0%" stopColor="var(--primary)" />
                    <stop offset="100%" stopColor="var(--success)" />
                  </linearGradient>
                </defs>
              </svg>
              <div style={styles.rateLabel}>
                <strong style={styles.rateNumber}>{scheduleRate}%</strong>
                <span style={styles.rateCaption}>scheduled</span>
              </div>
            </div>
          </div>
          <div style={styles.statGrid}>
            <Metric label="Total records" value={currentSummary.Total} tone="neutral" icon="#" />
            <Metric label="Scheduled" value={currentSummary.Scheduled} tone="good" icon="✓" />
            <Metric label="Original capacity gaps" value={currentSummary.OriginalUnscheduled} tone="warn" icon="!" />
            <Metric label="Disruption-created gaps" value={currentSummary.DisruptionUnscheduled} tone="bad" icon="⚡" />
          </div>
          <div style={styles.baselineStrip}>
            <span style={styles.pillNeutral}>Page-load baseline</span>
            <strong>{baselineSummary?.Scheduled || 0}</strong> scheduled
            <span style={styles.dotSep}>/</span>
            <strong>{baselineSummary?.Unscheduled || 0}</strong> unscheduled
          </div>
        </section>

        {/* ---------- Control + Diff ---------- */}
        <section style={styles.twoColumn}>
          <div className="animate-slide-up" style={styles.section}>
            <div style={styles.sectionHeader}>
              <div>
                <h2 style={styles.h2}>Disruption Control</h2>
                <p style={styles.muted}>{KIND_SUMMARIES[kind]}</p>
                <div style={styles.cutoffNote}>
                  ⏰ Same-day cutoff: <strong>{operatingDayEnd}</strong> IST. Interviews that cannot fit stay unscheduled.
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
                  <option value="student_dropout">🎓 Student dropout</option>
                  <option value="panel_dropout">👥 Panel dropout</option>
                  <option value="late_company">🕐 Late company</option>
                  <option value="room_unavailable">🚪 Room unavailable</option>
                </select>
              </label>

              <QuickPick interviews={interviews} kind={kind} onSelect={handleQuickSelect} />

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
                {submitting ? "Applying…" : "⚡ Apply disruption"}
              </button>
            </form>

            {submitError && (
              <div style={styles.errorBox}>
                <strong>Error:</strong> {submitError}
              </div>
            )}
          </div>

          <div className="animate-slide-up" style={styles.section}>
            <div style={styles.sectionHeader}>
              <div>
                <h2 style={styles.h2}>Activity / Diff Log</h2>
                <p style={styles.muted}>
                  Full blast radius reported by the replan call. Replans never spill into tomorrow; the active day closes at {operatingDayEnd}.
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

        {/* ---------- Browser ---------- */}
        <section className="animate-slide-up" style={styles.section}>
          <div style={styles.sectionHeader}>
            <div>
              <h2 style={styles.h2}>Schedule Browser</h2>
              <p style={styles.muted}>
                Paginated view of the current schedule. Filter by student, company, panel, room, or reason.
              </p>
            </div>
            <div style={styles.browserTools}>
              <input
                style={{ ...styles.input, width: 250 }}
                placeholder="🔍 Search IDs or reasons..."
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

          {loading && <div style={styles.emptyState}>Loading schedule…</div>}
          {error && (
            <div style={styles.errorBox}>
              <strong>Could not load schedule:</strong> {error}
              <div style={{ marginTop: 6, fontSize: 13, opacity: 0.8 }}>
                Start the backend with <code style={styles.codeInline}>go run ./cmd/server</code>.
              </div>
            </div>
          )}

          {!loading && !error && (
            <>
              <div style={styles.paginationBar}>
                <span>
                  Showing {filtered.length === 0 ? 0 : (safePage - 1) * PAGE_SIZE + 1}
                  –{Math.min(safePage * PAGE_SIZE, filtered.length)} of {filtered.length}
                </span>
                <div style={styles.pageButtons}>
                  <button
                    onClick={() => setPage((p) => Math.max(1, p - 1))}
                    disabled={safePage === 1}
                    style={styles.pageButton}
                  >
                    ← Prev
                  </button>
                  <strong style={styles.pageIndicator}>Page {safePage} / {pageCount}</strong>
                  <button
                    onClick={() => setPage((p) => Math.min(pageCount, p + 1))}
                    disabled={safePage === pageCount}
                    style={styles.pageButton}
                  >
                    Next →
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
                      <tr key={iv.ID} style={styles.tableRow}>
                        <td style={styles.tdStrong}>{iv.StudentID || "-"}</td>
                        <td style={styles.td}>{iv.CompanyID || "-"}</td>
                        <td style={styles.td}>{iv.PanelID || "-"}</td>
                        <td style={styles.td}>{iv.RoomID || "-"}</td>
                        <td style={styles.tdMono}>{fmtTime(iv.StartTime)}</td>
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

        <footer style={styles.footer}>
          PlacementOps — internship skill assessment · Go + React · {new Date().getFullYear()}
        </footer>
      </main>
    </div>
  );
}

/* ---------- Styles (theme-aware via CSS variables) ---------- */

const styles = {
  page: {
    minHeight: "100vh",
    boxSizing: "border-box",
    padding: 0,
    background: "var(--bg)",
    color: "var(--text-primary)",
    fontFamily: "'Inter', system-ui, sans-serif",
    textAlign: "left",
    position: "relative",
    overflowX: "hidden",
  },
  orb: {
    position: "absolute",
    borderRadius: "50%",
    filter: "blur(90px)",
    opacity: 0.35,
    pointerEvents: "none",
    zIndex: 0,
  },
  hero: {
    position: "relative",
    zIndex: 1,
    display: "flex",
    justifyContent: "space-between",
    alignItems: "center",
    gap: 18,
    padding: "34px 36px",
    background: "linear-gradient(120deg, rgba(102,126,234,0.16) 0%, rgba(118,75,162,0.12) 55%, rgba(240,147,251,0.08) 100%)",
    borderBottom: "1px solid var(--border)",
    backdropFilter: "blur(10px)",
    flexWrap: "wrap",
  },
  heroBadge: {
    display: "inline-flex",
    alignItems: "center",
    gap: 8,
    padding: "5px 14px",
    borderRadius: 999,
    background: "linear-gradient(135deg, rgba(99,102,241,0.18), rgba(139,92,246,0.12))",
    border: "1px solid var(--primary-200)",
    color: "var(--primary)",
    fontSize: 11,
    fontWeight: 700,
    letterSpacing: "1.2px",
    textTransform: "uppercase",
    marginBottom: 14,
  },
  pulseDot: {
    width: 7,
    height: 7,
    borderRadius: 999,
    background: "var(--success)",
    boxShadow: "0 0 0 3px var(--success-glow)",
    animation: "pulse 2s infinite",
  },
  heroContent: { minWidth: 280 },
  h1: {
    margin: 0,
    fontSize: 34,
    lineHeight: 1.1,
    fontWeight: 900,
    color: "var(--text-primary)",
    letterSpacing: "-1px",
    background: "linear-gradient(120deg, var(--text-primary) 30%, var(--primary) 100%)",
    WebkitBackgroundClip: "text",
    WebkitTextFillColor: "transparent",
    backgroundClip: "text",
  },
  h2: { margin: 0, fontSize: 17, fontWeight: 800, color: "var(--text-primary)" },
  h3: { margin: 0, fontSize: 13, fontWeight: 700, color: "var(--text-secondary)" },
  subhead: { margin: "8px 0 0", color: "var(--text-muted)", fontSize: 13, lineHeight: 1.55, maxWidth: 560 },
  muted: { margin: "4px 0 0", color: "var(--text-muted)", fontSize: 12, lineHeight: 1.5 },
  mutedTight: { margin: "2px 0 0", color: "var(--text-muted)", fontSize: 11, lineHeight: 1.4 },
  cutoffNote: {
    display: "inline-flex",
    alignItems: "center",
    gap: 6,
    marginTop: 10,
    padding: "6px 12px",
    borderRadius: 999,
    background: "var(--warning-light)",
    color: "var(--warning-dark)",
    fontSize: 11,
    fontWeight: 600,
    border: "1px solid var(--warning-glow)",
  },
  heroActions: { display: "flex", gap: 10, flexWrap: "wrap", justifyContent: "flex-end", alignItems: "center" },
  main: { position: "relative", zIndex: 1, display: "flex", flexDirection: "column", gap: 18, padding: "22px 28px 0", maxWidth: 1320, margin: "0 auto", width: "100%", boxSizing: "border-box" },

  /* metrics */
  section: {
    background: "var(--bg-elevated)",
    border: "1px solid var(--border)",
    borderRadius: 18,
    padding: 22,
    boxShadow: "var(--shadow-sm)",
    transition: "background-color var(--transition), border-color var(--transition)",
  },
  sectionHeader: {
    display: "flex",
    justifyContent: "space-between",
    gap: 14,
    alignItems: "flex-start",
    marginBottom: 18,
    flexWrap: "wrap",
  },
  twoColumn: {
    display: "grid",
    gridTemplateColumns: "minmax(300px, 0.42fr) minmax(420px, 0.58fr)",
    gap: 18,
    alignItems: "start",
  },
  rateRing: { position: "relative", width: 64, height: 64 },
  rateLabel: {
    position: "absolute",
    inset: 0,
    display: "flex",
    flexDirection: "column",
    alignItems: "center",
    justifyContent: "center",
  },
  rateNumber: { fontSize: 15, lineHeight: 1, color: "var(--text-primary)" },
  rateCaption: { fontSize: 8, color: "var(--text-muted)", textTransform: "uppercase", letterSpacing: "0.5px" },
  statGrid: {
    display: "grid",
    gridTemplateColumns: "repeat(auto-fit, minmax(180px, 1fr))",
    gap: 12,
  },
  metric: {
    borderTop: "3px solid",
    borderRadius: 14,
    padding: "14px 16px",
    background: "var(--gradient-card)",
    border: "1px solid var(--border)",
    transition: "transform var(--transition-fast), box-shadow var(--transition-fast)",
  },
  metricTop: { display: "flex", justifyContent: "space-between", alignItems: "center", gap: 8 },
  metricIcon: {
    display: "inline-flex",
    alignItems: "center",
    justifyContent: "center",
    width: 26,
    height: 26,
    borderRadius: 8,
    fontSize: 12,
    fontWeight: 800,
  },
  metricValue: { fontSize: 28, fontWeight: 900, lineHeight: 1, letterSpacing: "-0.5px" },
  metricLabel: { marginTop: 8, fontSize: 10, color: "var(--text-muted)", textTransform: "uppercase", fontWeight: 700, letterSpacing: "0.8px" },
  baselineStrip: {
    display: "flex",
    alignItems: "center",
    gap: 8,
    marginTop: 14,
    padding: "10px 14px",
    borderRadius: 12,
    background: "var(--surface-3, var(--bg))",
    border: "1px solid var(--border)",
    color: "var(--text-secondary)",
    fontSize: 12,
    flexWrap: "wrap",
  },
  pillNeutral: {
    background: "var(--gradient-primary)",
    color: "#fff",
    borderRadius: 999,
    padding: "3px 10px",
    fontSize: 10,
    fontWeight: 700,
    textTransform: "uppercase",
    letterSpacing: "0.5px",
  },
  dotSep: { color: "var(--text-muted)" },

  /* form */
  form: { display: "flex", flexDirection: "column", gap: 12 },
  label: {
    display: "flex",
    flexDirection: "column",
    gap: 5,
    color: "var(--text-secondary)",
    fontSize: 12,
    fontWeight: 600,
  },
  input: {
    border: "1.5px solid var(--border)",
    borderRadius: 10,
    padding: "9px 12px",
    fontSize: 13,
    background: "var(--bg)",
    color: "var(--text-primary)",
    boxSizing: "border-box",
    transition: "border-color var(--transition-fast), box-shadow var(--transition-fast)",
    outline: "none",
    fontFamily: "inherit",
  },
  primaryButton: {
    border: "none",
    borderRadius: 12,
    padding: "12px 18px",
    background: "linear-gradient(135deg, #667eea 0%, #764ba2 100%)",
    color: "#fff",
    fontWeight: 700,
    fontSize: 13,
    cursor: "pointer",
    boxShadow: "0 4px 14px rgba(102,126,234,0.4)",
    transition: "transform var(--transition-fast), box-shadow var(--transition-fast)",
  },
  secondaryButton: {
    border: "1.5px solid var(--border-strong)",
    borderRadius: 10,
    padding: "9px 14px",
    background: "var(--bg-elevated)",
    color: "var(--text-secondary)",
    fontWeight: 600,
    fontSize: 13,
    cursor: "pointer",
    transition: "all var(--transition-fast)",
  },
  undoButton: {
    border: "1.5px solid var(--warning-glow)",
    borderRadius: 10,
    padding: "9px 14px",
    background: "var(--warning-light)",
    color: "var(--warning-dark)",
    fontWeight: 700,
    fontSize: 13,
    cursor: "pointer",
    transition: "all var(--transition-fast)",
  },
  themeToggle: {
    display: "inline-flex",
    alignItems: "center",
    gap: 7,
    border: "1.5px solid var(--primary-200)",
    borderRadius: 10,
    padding: "9px 14px",
    background: "var(--primary-50)",
    color: "var(--primary-dark)",
    fontWeight: 700,
    fontSize: 12,
    cursor: "pointer",
    transition: "all var(--transition-fast)",
  },
  themeToggleIcon: { fontSize: 14 },
  themeToggleLabel: { fontSize: 12 },

  errorBox: {
    marginTop: 12,
    background: "var(--danger-light)",
    color: "var(--danger-dark)",
    padding: 12,
    borderRadius: 12,
    fontSize: 12,
    border: "1px solid var(--danger-glow)",
    wordBreak: "break-word",
  },
  codeInline: {
    background: "var(--surface-3, var(--bg))",
    padding: "1px 6px",
    borderRadius: 6,
    fontFamily: "'JetBrains Mono', monospace",
    fontSize: 12,
  },

  /* deltas */
  deltaGrid: {
    display: "grid",
    gridTemplateColumns: "repeat(3, minmax(0, 1fr))",
    gap: 10,
  },
  deltaMetric: {
    border: "1px solid var(--border)",
    borderRadius: 12,
    padding: 12,
    background: "var(--gradient-card)",
    minWidth: 0,
  },
  deltaFlow: {
    display: "flex",
    gap: 6,
    alignItems: "baseline",
    marginTop: 6,
    fontSize: 17,
    fontWeight: 700,
    color: "var(--text-primary)",
  },
  deltaArrow: { color: "var(--text-muted)", fontSize: 13 },
  deltaText: { marginTop: 3, fontSize: 11, fontWeight: 700 },

  /* reasons */
  reasonBox: { marginTop: 16, borderTop: "1px solid var(--border)", paddingTop: 16 },
  reasonHeader: { display: "flex", justifyContent: "space-between", gap: 10, alignItems: "center" },
  smallCaps: {
    color: "var(--text-muted)",
    fontSize: 10,
    letterSpacing: "0.8px",
    textTransform: "uppercase",
    fontWeight: 700,
  },
  reasonRows: { display: "flex", flexDirection: "column", marginTop: 8 },
  reasonRow: {
    display: "grid",
    gridTemplateColumns: "8px minmax(150px, 1fr) auto auto",
    gap: 10,
    alignItems: "center",
    fontSize: 12,
    padding: "9px 0",
    borderBottom: "1px solid var(--border)",
    color: "var(--text-secondary)",
  },
  reasonDotOriginal: {
    width: 8, height: 8, borderRadius: 8,
    background: "var(--warning)",
    boxShadow: "0 0 8px var(--warning-glow)",
  },
  reasonDotDisruption: {
    width: 8, height: 8, borderRadius: 8,
    background: "var(--danger)",
    boxShadow: "0 0 8px var(--danger-glow)",
  },
  reasonText: { color: "var(--text-secondary)", fontWeight: 500 },
  reasonLabelOriginal: {
    background: "var(--warning-light)",
    color: "var(--warning-dark)",
    borderRadius: 999,
    padding: "3px 9px",
    fontSize: 10,
    fontWeight: 700,
  },
  reasonLabelDisruption: {
    background: "var(--danger-light)",
    color: "var(--danger-dark)",
    borderRadius: 999,
    padding: "3px 9px",
    fontSize: 10,
    fontWeight: 700,
  },

  /* diffs */
  diffPanel: { marginTop: 16, borderTop: "1px solid var(--border)", paddingTop: 16 },
  diffSummary: {
    display: "flex",
    flexDirection: "column",
    gap: 4,
    padding: 14,
    borderRadius: 12,
    background: "linear-gradient(135deg, var(--primary-50), transparent)",
    color: "var(--primary-dark)",
    border: "1px solid var(--primary-200)",
    fontSize: 12,
  },
  diffList: { display: "flex", flexDirection: "column", gap: 10, marginTop: 12, maxHeight: 480, overflowY: "auto", paddingRight: 4 },
  diffCard: {
    border: "1px solid var(--border)",
    borderRadius: 12,
    padding: 14,
    background: "var(--bg-elevated)",
    transition: "box-shadow var(--transition-fast), transform var(--transition-fast)",
  },
  diffTopLine: { display: "flex", justifyContent: "space-between", gap: 10, alignItems: "flex-start" },
  diffTitle: { margin: "3px 0 0", fontSize: 14, fontWeight: 700, color: "var(--text-primary)" },
  outcomeCancelled: {
    background: "var(--danger-light)",
    color: "var(--danger-dark)",
    borderRadius: 999,
    padding: "3px 10px",
    fontSize: 10,
    fontWeight: 700,
    whiteSpace: "nowrap",
  },
  outcomeMoved: {
    background: "var(--success-light)",
    color: "var(--success-dark)",
    borderRadius: 999,
    padding: "3px 10px",
    fontSize: 10,
    fontWeight: 700,
    whiteSpace: "nowrap",
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
    border: "1px solid var(--primary-200)",
    background: "var(--primary-50)",
    borderRadius: 10,
    padding: 10,
    minWidth: 0,
    fontSize: 11,
    color: "var(--primary-dark)",
  },
  slotBlockEmpty: {
    display: "flex",
    flexDirection: "column",
    gap: 3,
    border: "1px solid var(--danger-glow)",
    background: "var(--danger-light)",
    borderRadius: 10,
    padding: 10,
    minWidth: 0,
    fontSize: 11,
    color: "var(--danger-dark)",
  },
  slotTitle: { color: "var(--text-muted)", fontSize: 9, fontWeight: 700, textTransform: "uppercase", letterSpacing: "0.8px" },
  compareArrow: { color: "var(--text-muted)", fontWeight: 900, fontSize: 15 },
  notifyRow: { display: "flex", gap: 6, flexWrap: "wrap", marginTop: 10 },
  notifyChip: {
    display: "inline-flex",
    gap: 4,
    alignItems: "center",
    borderRadius: 8,
    padding: "4px 9px",
    fontSize: 10,
    fontWeight: 600,
  },
  notifyStudent: { background: "var(--info-light)", color: "var(--info-dark)", border: "1px solid var(--info-glow)" },
  notifyPanel: { background: "var(--primary-50)", color: "var(--primary-dark)", border: "1px solid var(--primary-200)" },
  notifyCoordinator: { background: "var(--warning-light)", color: "var(--warning-dark)", border: "1px solid var(--warning-glow)" },

  /* activity */
  activityList: { display: "flex", flexDirection: "column", gap: 8, marginTop: 16 },
  activityItem: {
    display: "grid",
    gridTemplateColumns: "32px 1fr",
    gap: 10,
    alignItems: "start",
    padding: 10,
    border: "1px solid var(--border)",
    borderRadius: 12,
    background: "var(--bg-elevated)",
    fontSize: 12,
  },
  activityCount: {
    display: "inline-flex",
    alignItems: "center",
    justifyContent: "center",
    width: 28,
    height: 28,
    borderRadius: 9,
    background: "var(--gradient-primary)",
    color: "#fff",
    fontWeight: 800,
    fontSize: 12,
  },

  /* quickpick */
  quickPick: {
    marginBottom: 4,
    border: "1px solid var(--border)",
    borderRadius: 12,
    background: "var(--bg-elevated)",
    overflow: "hidden",
  },
  quickPickHeader: {
    display: "flex",
    justifyContent: "space-between",
    alignItems: "center",
    padding: "8px 12px",
    background: "linear-gradient(135deg, var(--primary-50), transparent)",
    fontSize: 10,
    fontWeight: 700,
    color: "var(--primary-dark)",
    textTransform: "uppercase",
    letterSpacing: "0.8px",
    borderBottom: "1px solid var(--border)",
  },
  quickPickCount: {
    background: "var(--gradient-primary)",
    color: "#fff",
    borderRadius: 999,
    minWidth: 20,
    height: 18,
    display: "inline-flex",
    alignItems: "center",
    justifyContent: "center",
    fontSize: 10,
    padding: "0 6px",
  },
  quickPickList: {
    display: "flex",
    flexDirection: "column",
    maxHeight: 190,
    overflowY: "auto",
  },
  quickPickItem: {
    display: "grid",
    gridTemplateColumns: "auto 1fr auto",
    gap: "4px 10px",
    alignItems: "center",
    padding: "8px 12px",
    border: "none",
    borderBottom: "1px solid var(--border)",
    background: "transparent",
    cursor: "pointer",
    textAlign: "left",
    fontSize: 12,
    color: "var(--text-primary)",
    transition: "background var(--transition-fast)",
    fontFamily: "inherit",
  },
  quickPickMain: {
    fontWeight: 700,
    color: "var(--text-primary)",
    fontFamily: "'JetBrains Mono', monospace",
    fontSize: 12,
  },
  quickPickSub: {
    color: "var(--text-muted)",
    fontSize: 11,
    whiteSpace: "nowrap",
    overflow: "hidden",
    textOverflow: "ellipsis",
  },
  quickPickTime: {
    color: "var(--primary)",
    fontWeight: 600,
    fontSize: 11,
    whiteSpace: "nowrap",
  },
  quickPickChevron: { color: "var(--text-muted)", fontWeight: 700 },

  /* browser */
  browserTools: { display: "flex", gap: 8, flexWrap: "wrap", justifyContent: "flex-end" },
  paginationBar: {
    display: "flex",
    justifyContent: "space-between",
    gap: 10,
    alignItems: "center",
    marginBottom: 12,
    color: "var(--text-secondary)",
    fontSize: 12,
    flexWrap: "wrap",
  },
  pageButtons: { display: "flex", alignItems: "center", gap: 8 },
  pageButton: {
    border: "1.5px solid var(--border)",
    borderRadius: 10,
    padding: "6px 12px",
    background: "var(--bg-elevated)",
    color: "var(--text-secondary)",
    fontWeight: 600,
    fontSize: 12,
    cursor: "pointer",
    transition: "all var(--transition-fast)",
  },
  pageIndicator: { fontSize: 12, color: "var(--text-secondary)" },
  tableWrap: {
    border: "1px solid var(--border)",
    borderRadius: 14,
    overflowX: "auto",
    boxShadow: "var(--shadow-sm)",
  },
  table: { width: "100%", borderCollapse: "collapse", fontSize: 12 },
  th: {
    textAlign: "left",
    padding: "11px 14px",
    background: "linear-gradient(180deg, var(--primary-50), transparent)",
    borderBottom: "2px solid var(--primary-200)",
    color: "var(--primary-dark)",
    fontSize: 10,
    fontWeight: 700,
    textTransform: "uppercase",
    letterSpacing: "0.8px",
    whiteSpace: "nowrap",
  },
  td: { padding: "10px 14px", borderBottom: "1px solid var(--border)", whiteSpace: "nowrap", color: "var(--text-secondary)" },
  tdStrong: {
    padding: "10px 14px",
    borderBottom: "1px solid var(--border)",
    whiteSpace: "nowrap",
    fontWeight: 700,
    color: "var(--text-primary)",
    fontFamily: "'JetBrains Mono', monospace",
    fontSize: 12,
  },
  tdMono: {
    padding: "10px 14px",
    borderBottom: "1px solid var(--border)",
    whiteSpace: "nowrap",
    color: "var(--text-secondary)",
    fontFamily: "'JetBrains Mono', monospace",
    fontSize: 12,
  },
  reasonCell: {
    padding: "10px 14px",
    borderBottom: "1px solid var(--border)",
    color: "var(--text-muted)",
    minWidth: 210,
    fontSize: 11,
  },

  badgeScheduled: {
    background: "var(--success-light)",
    color: "var(--success-dark)",
    borderRadius: 999,
    padding: "3px 10px",
    fontSize: 11,
    fontWeight: 700,
    whiteSpace: "nowrap",
  },
  badgeCancelled: {
    background: "var(--surface-3, var(--bg))",
    color: "var(--text-muted)",
    borderRadius: 999,
    padding: "3px 10px",
    fontSize: 11,
    fontWeight: 700,
    whiteSpace: "nowrap",
    border: "1px solid var(--border)",
  },
  badgeOriginal: {
    background: "var(--warning-light)",
    color: "var(--warning-dark)",
    borderRadius: 999,
    padding: "3px 10px",
    fontSize: 11,
    fontWeight: 700,
    whiteSpace: "nowrap",
  },
  badgeDisruption: {
    background: "var(--danger-light)",
    color: "var(--danger-dark)",
    borderRadius: 999,
    padding: "3px 10px",
    fontSize: 11,
    fontWeight: 700,
    whiteSpace: "nowrap",
  },
  emptyState: {
    marginTop: 10,
    padding: 16,
    border: "1.5px dashed var(--border-strong)",
    borderRadius: 12,
    background: "var(--surface-3, var(--bg))",
    color: "var(--text-muted)",
    fontSize: 12,
    textAlign: "center",
  },
  footer: {
    textAlign: "center",
    padding: "26px 0 8px",
    color: "var(--text-muted)",
    fontSize: 11,
    letterSpacing: "0.4px",
  },
};
