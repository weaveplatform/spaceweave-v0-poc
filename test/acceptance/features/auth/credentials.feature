@auth
Feature: Surface credentials
  Product and operator credentials grant access to different REST surfaces.
  Protecting the product surface also protects legacy routes and portal files.

  Scenario Outline: Credentials cannot cross API surfaces
    Given a running SpaceWeave server with separate product and operator credentials
    When a "<credential>" client sends "GET" to "<path>"
    Then the response is a 401 problem "unauthorized"
    And the response challenges for a bearer credential

    Examples:
      | credential | path                 |
      | anonymous  | /api/v1/info         |
      | operator   | /api/v1/info         |
      | product    | /operator/v1/runtime |
      | anonymous  | /operator/v1/runtime |
      | anonymous  | /api/info            |

  Scenario Outline: File aliases cannot bypass product authentication
    Given a running SpaceWeave server with separate product and operator credentials
    When an anonymous client sends "GET" to "<path>"
    Then the response is a 401 problem "unauthorized"

    Examples:
      | path                         |
      | /generated/catalog.json      |
      | /%67enerated/catalog.json     |
      | /generated%2Fcatalog.json     |
      | /GENERATED/catalog.json      |
      | //generated/catalog.json     |
      | /index.html                  |
