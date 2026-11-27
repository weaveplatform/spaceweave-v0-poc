@cli @product
Feature: Remote floorplan review
  spaceweavectl uses the public SDK and product REST API, without access to the
  server's local catalogue. Operator diagnostics use the separate operator API.

  Scenario: Import, inspect and approve a floorplan through the remote CLI
    Given a running SpaceWeave server with separate product and operator credentials
    When the remote CLI uploads an office drawing
    Then the drawing reaches draft processing state
    And the remote CLI lists the imported floorplan
    And the remote CLI downloads its model through the product API
    When the remote CLI approves the floorplan
    Then the remote CLI reads an approved floorplan
    And the operator runtime reports 1 approved floorplan
    And server logs contain no credentials
