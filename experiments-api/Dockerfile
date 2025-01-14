FROM node:22-alpine
WORKDIR /app

ARG PSQL_CONNECTION_STRING=postgresql://postgres:postgres@localhost:5432/db
ENV PSQL_CONNECTION_STRING=${PSQL_CONNECTION_STRING}

COPY . .
RUN npm install

CMD ["npm", "run", "start"]
