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

app.get("/api/experiments", async (req, res) => {
  try {
    const result = await pool.query("SELECT * FROM experiments");
    res.json(result.rows);
  } catch (error) {
    console.error("Error executing query", error);
    res.status(500).send({ message: "Error executing query" });
  }
});

app.listen(port, () => {
  console.log(`Server running on port ${port}`);
});
