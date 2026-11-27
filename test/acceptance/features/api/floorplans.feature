@product
Feature: Product floorplans
  Product consumers import and review floorplans through the versioned REST
  surface. Processing describes the current attempt; artifacts are read through
  supported URLs rather than paths in the server's filesystem.

  Scenario: Uploading the same drawing returns the existing floorplan
    Given a running SpaceWeave server with separate product and operator credentials
    When the remote CLI uploads an office drawing
    And the remote CLI uploads the same drawing again
    Then the upload identifies the existing floorplan as a duplicate
    And the product catalogue contains exactly 1 floorplan

  Scenario: Pagination rejects a cursor that does not belong to the contract
    Given a running SpaceWeave server in local mode
    When an anonymous client sends "GET" to "/api/v1/floorplans?cursor=%25%25"
    Then the response is a 400 problem "invalid-request"
