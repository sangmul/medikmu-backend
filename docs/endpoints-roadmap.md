# Endpoint roadmap

All under `/api/v1`. Status: ☐ todo, ☑ in `api/openapi.yaml`. Derived from `medikmu-web` and `medikmu_mobile`.

## Phase 1 — Foundation
- ☑ `POST /auth/register`, `/auth/login`, `/auth/refresh`, `/auth/logout`, `/auth/switch-facility`, `GET /me` (implemented: `internal/auth`)
- ☐ `POST /auth/forgot-password`, `/auth/verify-otp` (need an email/SMS/WhatsApp provider), login rate limiting (Redis), invite acceptance (staff onboarding)
- ☐ `POST /files` (signed upload), `GET /files/{id}`
- ☐ `POST /devices` (FCM token), `GET /notifications`
- ☑ Facilities and team (implemented: `internal/facilities`): `POST /facilities`, `GET|PUT /facilities/current`, `GET /team`, `DELETE /team/{userId}`, `GET|POST /invites`, `DELETE /invites/{id}`, `POST /invites/accept`
- ☐ Team later: change a member's role, leave a facility, resend/expire invites by email (needs an email provider), facility logo/settings

## Phase 2 — Core care
- ☑ Patients (implemented: `internal/patients`): `GET|POST /patients`, `GET|PUT|DELETE /patients/{id}` (DELETE archives, admin only)
- ☐ Patients later: link to a patient app account (`patients.user_id`), duplicate-person warning (name + birth date), merge, import
- ☐ **Doctors, appointments and booking: design ready, needs decisions: [design/appointments.md](design/appointments.md)**
- ☐ Doctors: `GET /doctors`, `/doctors/{id}`, `/doctors/{id}/slots`, `GET|PUT /schedules`
- ☐ Appointments (web): `GET /appointments` (day/week/month), `POST`, `PATCH /{id}/status`
- ☐ Booking (mobile): `POST /bookings`, `GET /bookings`, `PATCH /bookings/{id}/cancel`
- ☐ Visits: `GET /visits`, `POST /visits/{id}/check-in`
- ☐ Records: `GET|POST /patients/{id}/records`, `/vitals`, `/diagnoses` (ICD-10), `/sketches`, `/odontograms`, `/ecg`
- ☐ Prescriptions: `POST /prescriptions`, `GET /prescriptions/{id}`, print PDF, compounds
- ☐ Labs: `GET|POST /lab-orders`, `GET /lab-results/{id}`, report PDF

## Phase 3 — Patient app
- ☑ `PUT /me/profile` (name, phone; `phone_verified` stays false until OTP exists)
- ☑ Family members (implemented: `internal/family`): `GET|POST /family-members`, `GET|PUT|DELETE /family-members/{id}`, `PUT /family-members/{id}/nik`
- ☐ `/me/records`, `/me/lab-results`, `/me/prescriptions`, `/me/vitals`, `/me/medications` (need the records, labs and prescriptions modules)
- ☐ Link a family member to a facility's patient record (`patients.user_id` is ready), BPJS card active status (needs BPJS integration)
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
