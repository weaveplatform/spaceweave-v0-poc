@operator
Feature: Operator runtime diagnostics
  Platform diagnostics have a separate credential. Local product mode does not
  implicitly enable operator access or provide business mutation authority.

  Scenario: A configured operator can read runtime diagnostics
    Given a running SpaceWeave server with separate product and operator credentials
    When an "operator" client sends "GET" to "/operator/v1/runtime"
    Then the response has HTTP status 200
    And runtime diagnostics identify the tracer and worker concurrency

  Scenario: Operator access is disabled until configured
    Given a running SpaceWeave server in local mode
    When an anonymous client sends "GET" to "/operator/v1/runtime"
    Then the response is a 503 problem "operator-disabled"

  Scenario: The remote CLI reports refused operator access
    Given a running SpaceWeave server with separate product and operator credentials
    When the remote CLI uses a product credential for operator runtime
    Then the CLI exits with a runtime failure and a 401 problem

  Scenario: Local product reads do not require an operator credential
    Given a running SpaceWeave server in local mode
    When an anonymous client sends "GET" to "/api/v1/info"
    Then the response has HTTP status 200
