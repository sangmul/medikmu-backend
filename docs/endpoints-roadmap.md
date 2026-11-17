# Endpoint roadmap

All under `/api/v1`. Status: ☐ todo, ☑ in `api/openapi.yaml`. Derived from `medikmu-web` and `medikmu_mobile`.

## Phase 1 — Foundation
- ☑ `POST /auth/register`, `/auth/login`, `/auth/refresh`, `/auth/logout`, `/auth/switch-facility`, `GET /me` (implemented: `internal/auth`)
- ☐ `POST /auth/forgot-password`, `/auth/verify-otp` (need an email/SMS/WhatsApp provider), login rate limiting (Redis), invite acceptance (staff onboarding)
- ☐ `POST /files` (signed upload), `GET /files/{id}`
- ☐ `POST /devices` (FCM token), `GET /notifications`
- ☐ `GET|PUT /facilities/current`, `/team`, `/invites`

## Phase 2 — Core care
- ☑ `GET /patients`; ☐ `POST /patients`, `GET|PUT /patients/{id}`
- ☐ Doctors: `GET /doctors`, `/doctors/{id}`, `/doctors/{id}/slots`, `GET|PUT /schedules`
- ☐ Appointments (web): `GET /appointments` (day/week/month), `POST`, `PATCH /{id}/status`
- ☐ Booking (mobile): `POST /bookings`, `GET /bookings`, `PATCH /bookings/{id}/cancel`
- ☐ Visits: `GET /visits`, `POST /visits/{id}/check-in`
- ☐ Records: `GET|POST /patients/{id}/records`, `/vitals`, `/diagnoses` (ICD-10), `/sketches`, `/odontograms`, `/ecg`
- ☐ Prescriptions: `POST /prescriptions`, `GET /prescriptions/{id}`, print PDF, compounds
- ☐ Labs: `GET|POST /lab-orders`, `GET /lab-results/{id}`, report PDF

## Phase 3 — Patient app
- ☐ `/me/profile`, `/me/records`, `/me/lab-results`, `/me/prescriptions`, `/me/vitals`, `/me/medications`
- ☐ `/family-members` (CRUD)
- ☐ KIA: `/kia/growth`, `/kia/immunizations`, `/kia/antenatal`
- ☐ Chat: `/conversations`, `/conversations/{id}/messages`, WebSocket `/ws`
- ☐ `GET /medicines`

## Phase 4 — Business
- ☐ Billing: `/bills`, `/tariffs`, `/recap`, `/subscription`
- ☐ Documents: `/documents` (sick notes etc.), public `GET /documents/verify/{code}`
- ☐ `GET /dashboard/stats` (per role)

## Phase 5 — Advanced (post-MVP)
Pharmacy (inventory, procurement, controlled drugs), inpatient (ward, surgery, discharge), emergency, accounting/tax, compliance (incident, waste, infection, SatuSehat reporting).

## MVP definition
Phases 1–3 minus advanced records (ECG, odontogram, sketches). Ship that to the first clinics; add the rest from feedback.
