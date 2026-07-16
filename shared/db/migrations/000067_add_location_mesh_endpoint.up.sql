-- A location participates in the connectivity mesh when it advertises an
-- endpoint (host:port) that other locations' workers can reach. The worker
-- serves GET /mesh/echo on its HTTP port; the admin points this column at
-- whatever address routes to it (Service, NLB, published docker port).
-- NULL/empty = the location is not part of the mesh.
ALTER TABLE locations
    ADD COLUMN mesh_endpoint TEXT;
