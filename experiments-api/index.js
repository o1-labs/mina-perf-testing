import cors from "cors";
import express from "express";
import pg from "pg";

const { Pool } = pg;
const app = express();
const port = process.env.PORT ?? 3003;

const pool = new Pool({
  connectionString: process.env.PSQL_CONNECTION_STRING,
});

app.use(cors());
app.use(express.json());

// GET /api/experiments
app.get("/api/experiments", async (req, res) => {
  try {
    const result = await pool.query("SELECT * FROM experiments");
    res.json(result.rows);
  } catch (error) {
    console.error("Error executing query", error);
    res.status(500).send({ message: "Error executing query" });
  }
});


// GET /api/experiment/{name}
app.get("/api/experiment/:name", async (req, res) => {
  const { name } = req.params;

  try {
    const result = await pool.query("SELECT * FROM experiment_state WHERE name = $1", [name]);
    res.json(result.rows);
  } catch (error) {
    console.error("Error executing query", error);
    res.status(500).send({ message: "Error executing query" });
  }
});

// POST /api/deployments
app.post('/api/deployments', async (req, res) => {
  const { data } = req.body;

  if (!data) {
    return res.status(400).json({ error: 'Missing "data" in request body' });
  }

  try {
    const result = await pool.query(
      'INSERT INTO deployment (metadata_json) VALUES ($1) RETURNING deployment_id',
      [data]
    );

    res.status(201).json({
      message: 'Deployment created',
      deployment_id: result.rows[0].deployment_id,
    });
  } catch (err) {
    console.error('Error inserting deployment:', err);
    res.status(500).json({ error: 'Internal Server Error' });
  }
});


// POST /api/node-restarts
app.post('/api/node-restarts', async (req, res) => {
  const { 
    node_name, 
    reason, 
    code, 
    object_name, 
    object_namespace, 
    container_name, 
    restart_count 
  } = req.body;

  if (!node_name) {
    return res.status(400).json({ error: 'Missing required field: "node_name"' });
  }

  try {
    const result = await pool.query(
      `INSERT INTO node_restarts (
        node_name, 
        reason, 
        code, 
        object_name, 
        object_namespace, 
        container_name, 
        restart_count
      ) VALUES ($1, $2, $3, $4, $5, $6, $7) 
      RETURNING id, time`,
      [
        node_name,
        reason || null,
        code || null,
        object_name || null,
        object_namespace || null,
        container_name || null,
        restart_count || null
      ]
    );

    res.status(201).json({
      message: 'Node restart recorded',
      id: result.rows[0].id,
      time: result.rows[0].time
    });
  } catch (err) {
    console.error('Error inserting node restart:', err);
    res.status(500).json({ error: 'Internal Server Error' });
  }
});


app.post('/api/tracing/cleanup', async (req, res) => {

  
  try {

    //-- Step 1: Delete from block_trace_checkpoint
    var _ = await pool.query(
      "DELETE FROM block_trace_checkpoint \
        USING block_trace \
        WHERE block_trace_checkpoint.block_trace_id = block_trace.block_trace_id \
        AND block_trace.deployment_id < (SELECT MAX(deployment_id) FROM deployment) \
        AND block_trace.status = 'Pending'");

    // -- Step 2: Delete from block_trace ONLY if there are no dependent checkpoints left
    var _ = await pool.query(
      "DELETE FROM block_trace \
        WHERE deployment_id < (SELECT MAX(deployment_id) FROM deployment) \
        AND status = 'Pending'");

    res.status(201).json({
      message: 'Historical data cleaned up'
    });
  } catch (err) {
    console.error('Error cleaning old deployments:', err);
    res.status(500).json({ error: 'Internal Server Error' });
  }

})


app.listen(port, () => {
  console.log(`Server running on port ${port}`);
});
