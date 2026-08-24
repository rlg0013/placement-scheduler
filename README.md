# Placement Scheduler

A real-time interview scheduling and live replanning system for college placement cells. Built with Go (backend) and React (frontend), this project solves the complex optimization problem of scheduling 800+ students across 35 companies with 20 rooms over a 4-day placement week — and handles real-time disruptions gracefully.

## Live Demo

```
Backend API:   http://localhost:8080
Frontend UI:   http://localhost:5173
```

## Problem Statement

College placement weeks are chaotic. Companies arrive late, panels drop out, rooms become unavailable, and students withdraw — all while hundreds of interviews need to happen in tight time windows. This system:

1. **Generates realistic placement data** — companies tiered by hiring volume (mass recruiters, mid-tier, niche), student CGPAs, shortlists, panels, and rooms
2. **Schedules interviews optimally** using an event-driven matching algorithm with panel room stickiness
3. **Handles live disruptions** by replanning affected interviews in real-time while preserving invariants
4. **Provides a control dashboard** for coordinators to trigger disruptions, view diffs, and undo changes

## Architecture

```
placement-scheduler/
├── backend/                          # Go 1.22 backend
│   ├── cmd/
│   │   ├── server/main.go            # HTTP API server (port 8080)
│   │   ├── scheduler-test/main.go    # Scheduler invariant validation
│   │   ├── replan-test/main.go       # Replan engine invariant validation
│   │   └── gen-test/main.go          # Data generation analysis
│   └── internal/
│       ├── models/models.go          # Core domain types
│       ├── generator/generator.go    # Synthetic data generator
│       ├── scheduler/scheduler.go    # Event-driven scheduling engine
│       ├── replan/
│       │   ├── replan.go             # 4 disruption handlers
│       │   └── dispatch.go           # Disruption type router
│       └── api/
│           ├── handlers.go           # HTTP endpoints
│           └── disruptions.go        # Request parsing
└── frontend/                         # React + Vite frontend
    └── src/
        └── App.jsx                   # Single-page control dashboard
```

## Key Features

### Data Generator (`generator.go`)

Generates realistic placement week data with configurable parameters:

| Parameter | Value | Description |
|-----------|-------|-------------|
| Companies | 35 | 8 mass recruiters, 17 mid-tier, 10 niche |
| Students | 800 | CGPA distributed ~N(7.2, 0.9) |
| Rooms | 20 | Single-panel rooms |
| Days | 4 | Sep 1–4, 2026 (IST) |

**Company tiers model real-world patterns:**
- **Mass Recruiters** (Day 1): Low CGPA cutoff (5.5–6.5), 4–6 panels, 15-min interviews, 60–90 shortlisted
- **Mid-Tier** (Day 2–3): Moderate cutoff (6.5–7.5), 2–3 panels, 25-min interviews, 30–55 shortlisted
- **Niche** (Day 3–4): High cutoff (7.5–8.8), 1–2 panels, 40-min interviews, 10–18 shortlisted

Student selection uses CGPA-weighted noise (tier-specific `selectionNoiseStd`) so top students appear on many overlapping lists — just like real placement season.

### Scheduling Engine (`scheduler.go`)

Event-driven matching using a **min-heap of panels** ordered by next-free time:

1. **Wave building**: Companies sharing the same `(day, start, end)` window are grouped into waves
2. **Panel admission**: Two-phase room allocation — guarantee one panel per company, then fill remaining rooms by priority
3. **Matching loop**: For each freed panel, find the next available student and room, book the interview, push the panel back with its new free time
4. **Room stickiness**: A panel stays in the same physical room for its entire day (no mid-day room switches)

**Invariants maintained:**
- No student double-booking (checked via `busyUntil` map)
- No room double-booking (checked via `roomBusyUntil` map)
- Panel room stickiness (one panel = one room per day)

### Replanning Engine (`replan.go`)

Handles 4 disruption types with clone-before-mutate safety:

| Disruption | What Happens | Strategy |
|------------|-------------|----------|
| **Student Dropout** | Cancel one interview | Compact later interviews on same panel forward |
| **Panel Dropout** | Remove one panel | Absorb displaced students into surviving panels |
| **Late Company** | Company arrives late | Re-derive schedule from arrival time, compress idle gaps |
| **Room Unavailable** | Room goes offline | Find replacement room per panel, re-match displaced students |

**Key design decisions:**
- Replans use `disruptionDayEnd()` (19:00 IST) as the hard cutoff — no interviews past 7 PM
- `RunMatching()` is shared between initial scheduling and replanning
- Each replan produces a `Diff` with before/after slots and notification targets
- Up to 10 history snapshots for undo support

### Frontend Dashboard (`App.jsx`)

Single-page control console with:

- **Schedule Overview**: Live metrics (total, scheduled, original gaps, disruption gaps)
- **Disruption Control**: Form to trigger any of the 4 disruption types
- **Diff Log**: Before/after comparison with notification targets
- **Schedule Browser**: Paginated, filterable table of all interviews
- **Undo**: Roll back to previous state

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/schedule` | Returns all interview records |
| `GET` | `/status` | Returns summary, undo count, operating hours |
| `POST` | `/disruptions` | Apply a disruption (triggers replan) |
| `POST` | `/undo` | Roll back last disruption |

### Disruption Request Format

```json
{
  "kind": "late_company",
  "at": "2026-09-01T12:00:00+05:30",
  "company_id": "MASS-01",
  "delay_minutes": 120
}
```

## Running the Project

### Prerequisites
- Go 1.22+
- Node.js 18+

### Backend
```bash
cd backend
go run ./cmd/server          # Starts API on :8080
go run ./cmd/scheduler-test  # Validate scheduling invariants
go run ./cmd/replan-test     # Validate replanning invariants
```

### Frontend
```bash
cd frontend
npm install
npm run dev    # Starts Vite dev server on :5173
```

## Tech Stack

| Layer | Technology |
|-------|-----------|
| Backend | Go 1.22, `net/http`, `container/heap` |
| Frontend | React 19, Vite, inline styles |
| Data | Synthetic generator (seeded RNG) |
| Timezone | IST (UTC+5:30) |

## Design Decisions

1. **Event-driven matching over greedy assignment**: The min-heap approach naturally handles panels becoming free at different times, avoiding the pitfalls of fixed-time-slot assignment.

2. **Room stickiness as a first-class invariant**: Real panels don't move rooms mid-day. The scheduler enforces this via `panelRoom` map rather than treating it as a soft preference.

3. **Clone-before-mutate for replanning**: Every replan clones the full state, attempts the change, and only commits if successful. This enables safe undo without complex rollback logic.

4. **UTC internally, IST for display**: All `time.Time` values use IST for correct 7 PM cutoff behavior. The frontend displays in the browser's local timezone.

5. **Priority tiers model real placement dynamics**: Niche companies (highest priority, fewest students) get scheduled first when there's contention, mirroring how real placement cells protect high-value recruiters.

## Validation

All scheduling invariants are automatically checked:

```bash
# Scheduler invariants
go run ./cmd/scheduler-test
# ✅ Student double-booking check PASSED
# ✅ Room double-booking check PASSED
# ✅ Panel room-stickiness check PASSED

# Replan invariants (each disruption type)
go run ./cmd/replan-test
# ✅ baseline OK
# ✅ after StudentDropout OK
# ✅ after PanelDropout OK
# ✅ after LateCompany OK
# ✅ after RoomUnavailable OK
```

## License

This project was developed as a skill assessment for an internship position.
