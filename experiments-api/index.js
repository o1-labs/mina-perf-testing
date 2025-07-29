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

// POST /api/deployments
app.post('/api/deployments', async (req, res) => {
  const { data } = req.body;
  const authKey = req.headers['authorization'];

  if (!authKey || authKey !== process.env.AUTH_KEY) {
    return res.status(403).json({ error: 'Forbidden: Invalid or missing authorization key' });
  }

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


app.listen(port, () => {
  console.log(`Server running on port ${port}`);
});
