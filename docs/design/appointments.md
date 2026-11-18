# Design: doctors, schedules, appointments and booking

Status: **proposal, needs decisions** (section 6). Not implemented. Derived from `medikmu-web`
(`features/appointments`) and `medikmu_mobile` (`features/booking`, `visits`, `doctors`).

## 1. What the two apps expect

**Web (clinic staff):** a calendar (day/week/month) of appointments with a status flow
`booked → waiting → in-room → (at-lab ⇄ back-from-lab) → done`; "requests" waiting for approval; doctors' weekly working
hours and **leave** (blocks booking, lists visits someone must move); a doctor sees only their own visits, front desk/nurse
see everyone's; moving a visit is blocked by overlap and by leave, and only *warned* when outside working hours.
Visit kinds: `inperson`, `new` (new patient), `open` (an open slot, not a visit).

**Mobile (patient):** search doctors (filter by specialty), pick a family member, visit type (`inperson` or `video`), a time slot,
a reason; get a **booking code** like `AP-4821` (also a check-in QR); "My visits" shows upcoming and past; check in on the day.
Fees differ for in-person and video.

## 2. Model (sketch)

```
doctor_profiles   (facility_id, user_id)  PK  — specialty, bio, slot_minutes, fee_in_person, fee_video,
                                               bookable boolean, listed_publicly boolean
doctor_hours      (id, facility_id, user_id, weekday 0-6, start_time, end_time)
doctor_leaves     (id, facility_id, user_id, from_date, to_date, reason)
appointments      (id, facility_id, doctor_user_id, patient_id NULL → patients,
                   booked_by_user_id NULL, family_member_id NULL,
                   starts_at timestamptz, minutes, kind, visit_type, status, reason, booking_code UNIQUE,
                   created_at, updated_at)
appointment_events(id, appointment_id, status, at, actor_id)             — audit of the status flow
```
Invariants enforced in the database, not only in Go: no two non-cancelled appointments of one doctor overlap
(`EXCLUDE USING gist (doctor_user_id WITH =, tstzrange(starts_at, ends_at) WITH &&) WHERE status <> 'cancelled'`,
needs `btree_gist`), so two patients can never take the same slot even under concurrency.

**Slots** are computed, not stored: doctor hours for the date, minus leave, minus existing appointments, cut into
`slot_minutes`, in the facility's time zone (Indonesia spans WIB/WITA/WIT, so `facilities.timezone` is needed; default `Asia/Jakarta`).

## 3. Endpoints (all under `/api/v1`)

Web / staff (active facility):
- `GET|PUT /doctors/{userId}/profile`, `GET|PUT /doctors/{userId}/hours`, `GET|POST /doctors/{userId}/leaves`, `DELETE /leaves/{id}`
- `GET /appointments?from=&to=&doctor=` (a doctor is auto-scoped to themselves), `POST /appointments`,
  `PATCH /appointments/{id}` (move), `POST /appointments/{id}/status` `{status}` (enforces the allowed transitions), `DELETE` = cancel
- `GET /appointment-requests`, `POST /appointment-requests/{id}/approve|decline`

Mobile / patient (personal context):
- `GET /doctors?specialty=&q=` (public directory, only `listed_publicly`), `GET /doctors/{facilityId}/{userId}`
- `GET /doctors/{facilityId}/{userId}/slots?date=`
- `POST /bookings` `{doctor, family_member_id, visit_type, starts_at, reason}` with `Idempotency-Key` → `{code, ...}`
- `GET /bookings` (mine), `PATCH /bookings/{id}/cancel`, `POST /visits/{id}/check-in`

## 4. Rules worth deciding now
- **Status transitions** are a small state machine in the service (`booked→waiting→in-room→at-lab→back-from-lab→in-room→done`,
  `booked→cancelled|no-show`), every change recorded in `appointment_events` and audited.
- **Idempotency**: `POST /bookings` takes `Idempotency-Key` so a retry on a flaky mobile connection never double-books.
- **Authorization**: staff see their facility only; a doctor only their own visits (like the web app); patients only their own bookings.
- **Leave**: adding leave over booked visits is allowed but returns the affected visits (the web app already works this way).

## 5. Suggested build order (about three small PRs)
1. Doctor profile + hours + leave + slot computation (pure, heavily tested), then `GET .../slots`.
2. Appointments for staff (calendar, status flow, overlap constraint, audit).
3. Mobile booking (`/bookings`, idempotency, check-in, directory).

## 6. Decisions needed (recommended default in bold)
1. **Does a booking confirm instantly or wait for the clinic to approve?** The web app has a "requests" list.
   **Instant for listed slots; "requests" only for a time outside working hours or a new-patient request.**
2. **When is a `patients` record created for a mobile booker?** **At check-in by the front desk**, from the family member's data
   with consent captured then. Creating it at booking would fill clinics' lists with no-shows.
3. **Who appears in the doctor directory?** **Opt-in per doctor (`listed_publicly`)**, searchable across facilities. The alternative is
   a private link/code per clinic.
4. **Video visits:** the app has `video` as a visit type, but a video provider is a separate integration (cost, privacy, recording).
   **Store the type and fee now; integrate a provider later.**
5. **Fees and payment:** **show fees only; no online payment in this phase** (needs a payment provider and accounting rules).
6. **Cancel and reschedule window** (for example free until 2 hours before)? **Cancel anytime until the visit starts; clinics may mark `no-show`.**
7. **Time zone:** **per-facility `timezone`, default `Asia/Jakarta`.**
