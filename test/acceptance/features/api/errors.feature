@product
Feature: Product request contracts
  Malformed requests and unsupported operations return machine-readable problem
  details. Mutation requests must carry the local browser mutation guard.

  Scenario Outline: Router and parameter failures use problem details
    Given a running SpaceWeave server in local mode
    When an anonymous client sends "<method>" to "<path>"
    Then the response is a <status> problem "<problem>"

    Examples:
      | method | path                       | status | problem            |
      | GET    | /api/v1/unknown            | 404    | not-found          |
      | PATCH  | /api/v1/floorplans         | 405    | method-not-allowed |
      | GET    | /api/v1/floorplans?limit=0 | 400    | invalid-request    |
      | GET    | /api/v1/floorplans?limit=x | 400    | invalid-request    |

  Scenario: A mutation without the required guard is rejected
    Given a running SpaceWeave server in local mode
    When an anonymous client sends "POST" to "/api/v1/floorplans"
    Then the response is a 400 problem "invalid-request"

  Scenario Outline: JSON property names must match the contract exactly
    Given a running SpaceWeave server in local mode
    When a client sends guarded JSON '<body>' to "/api/v1/floorplans/missing/reject"
    Then the response is a 400 problem "invalid-request"

    Examples:
      | body                  |
      | {"NOTE":"review"}     |
      | {"unknown":"review"}  |
