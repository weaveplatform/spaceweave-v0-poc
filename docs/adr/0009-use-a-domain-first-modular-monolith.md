---
status: accepted
---

# Use a domain-first modular monolith

Choose [Option 5](../research/architecture-options/5-modular-monolith.md): business
modules with published interfaces and owned data in one application release,
using ports and adapters inside substantial modules. This keeps each capability's
behaviour and data ownership together without requiring uniform layers for small
modules. The [five-option comparison](../research/architecture-options/README.md)
records the alternatives and tradeoffs. Import restrictions and explicit
cross-module transaction contracts are required; directory names alone do not
enforce ownership.

The existing floor-plan tool is the first module, named **Floorplans** with the
namespace `internal/floorplans`. Its processing algorithms, local catalogue,
workers, import and review behaviour are retained behind module assembly and a
published in-process API. Application assembly lives in `internal/app`, portal
hosting in `internal/gateway/portal`, and build metadata in `internal/platform`.

Accepted future owners combine [Identity](0026-combine-workforce-identity-and-access-ownership.md),
keep [planning in Booking](0027-keep-attendance-planning-inside-booking.md), and
separate [Exchange](0028-separate-exchange-synchronisation-from-booking.md) and
[Events](0023-separate-events-from-venue-booking.md). The booking platform,
PostgreSQL durability, cloud assets, React portals and independently scaled
Kubernetes roles remain future implementation work. Their concrete publication,
recovery and transaction protocols still require validation.
