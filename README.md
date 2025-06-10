# Flash Sale API

This project implements a high-performance Flash Sale API in Go. It is designed to handle a large volume of concurrent requests for a limited number of items, a common scenario in flash sales or high-demand product launches.

The system uses Redis for fast, in-memory operations like creating reservations and checking limits, and PostgreSQL for persistent storage of checkout attempts and confirmed sales. The application is containerized using Docker for easy setup and deployment.

-----

## Prerequisites

Before you begin, ensure you have the following installed:

  * [**Docker**](https://docs.docker.com/get-docker/) and [**Docker Compose**](https://docs.docker.com/compose/install/)
  * [**Go**](https://golang.org/doc/install/) (1.23 or later)
  * [**k6**](https://grafana.com/docs/k6/latest/get-started/) (for performance testing)

-----

## Building and Running the Application

The recommended way to run the application is with Docker Compose, which orchestrates the Go application, Postgres database, and Redis cache.

1.  **Clone the Repository**:

    ```bash
    git clone https://github.com/awtifalak/flash.git
    cd flash
    ```

2.  **Start the Services**:
    Use Docker Compose to build the application container and start all services in detached mode.

    ```bash
    docker compose up --build -d
    ```

    This command will:

      * Build the Docker image for the Go application based on the `Dockerfile`.
      * Start three services: `app`, `postgres`, and `redis`.
      * Set up a dedicated network (`flashsale-net`) for the services to communicate.
      * The application will be accessible on `http://localhost:8080`.

3.  **Verify the Application is Running**:
    You can check the logs to ensure everything started correctly.

    ```bash
    docker compose logs -f app
    ```

    You should see a message indicating the server has started:

    ```
    Starting HTTP server on :8080
    ```

-----

## API Endpoints

The service exposes the following HTTP endpoints:

#### `POST /checkout`

Initiates a checkout attempt and creates a reservation if successful.

  * **Query Parameters**:
      * `user_id` (string): The ID of the user.
      * `id` (string): The ID of the item.
  * **Success Response** (`200 OK`):
    ```json
    {
      "message": "success",
      "code": "a_unique_reservation_code"
    }
    ```
  * **Example**:
    ```bash
    curl -X POST "http://localhost:8080/checkout?user_id=user123&id=item456"
    ```

#### `POST /purchase`

Processes the purchase using a valid reservation code.

  * **Query Parameters**:
      * `code` (string): The reservation code obtained from `/checkout`.
  * **Success Response** (`200 OK`):
    ```json
    {
      "message": "success",
      "user": "user123",
      "item": "item456"
    }
    ```
  * **Example**:
    ```bash
    curl -X POST "http://localhost:8080/purchase?code=a_unique_reservation_code"
    ```

#### `GET /status`

Retrieves the current status of the flash sale, including metrics on checkouts and purchases.

  * **Success Response** (`200 OK`):
    ```json
    {
      "seconds_remaining": 3540,
      "successful_checkouts": 500,
      "failed_checkouts": 120,
      "successful_purchases": 498,
      "failed_purchases": 2,
      "scheduled_goods": 500,
      "purchased_goods": 498,
      "sale_status": "active"
    }
    ```
  * **Example**:
    ```bash
    curl -X GET "http://localhost:8080/status"
    ```

-----

## Performance Testing with k6

To simulate high traffic and test the system's performance, you can use `k6`. Below is a test script that simulates a typical flash sale scenario where many users attempt to check out, and a smaller number proceed to purchase.

1.  **Create a Test Script**:
    Save the following code as `performance-test.js`:

    ```javascript
    import http from 'k6/http';
    import { check, sleep } from 'k6';

    // Configurable
    const NUM_ITEMS = 500;

    export let options = {
        stages: [
            { duration: '10s', target: 10 },  // ramp up
            { duration: '30s', target: 10 },  // stay at 10 users
            { duration: '10s', target: 0 },   // ramp down
        ],
    };

    const BASE_URL = 'http://localhost:8080';
    const ITEMS = Array.from({ length: NUM_ITEMS }, (_, i) => `item${i + 1}`);

    export default function () {
        const userId = `user${__VU}-${__ITER}`;

        // Compute unique item per iteration
        const itemIndex = (__VU - 1) * 100 + __ITER; // 100 iterations per VU
        if (itemIndex >= ITEMS.length) return; // No more items to process

        const itemId = ITEMS[itemIndex];

        const checkoutRes = http.post(`${BASE_URL}/checkout?user_id=${userId}&id=${itemId}`);

        check(checkoutRes, {
            'checkout: status is 200': (r) => r.status === 200,
            'checkout: has reservation code': (r) => r.json('code') !== undefined,
        });

        const reservationCode = checkoutRes.json('code');

        sleep(Math.random() * 2);

        const purchaseRes = http.post(`${BASE_URL}/purchase?code=${reservationCode}`);

        check(purchaseRes, {
            'purchase: status is 200': (r) => r.status === 200,
            'purchase: message is success': (r) => r.json('message') === 'success',
        });

        sleep(Math.random() * 2);

        if (__ITER % 5 === 0) {
            const statusRes = http.get(`${BASE_URL}/status`);

            check(statusRes, {
                'status: status is 200': (r) => r.status === 200,
                'status: has sale_status field': (r) => r.json('sale_status') !== undefined,
            });
        }

        sleep(Math.random() * 1);
    }
    ```

2.  **Run the Test**:
    Execute the following command in your terminal:

    ```bash
    k6 run performance-test.js
    ```

3.  **Monitor the Results**:

      * **k6 Output**: While the test is running, `k6` will display real-time metrics, including the number of requests per second, response times, and success rates.
      * **Application Status**: You can simultaneously monitor the `/status` endpoint to see how the application is handling the load in real-time.
        ```bash
        # Watch the status endpoint every second
        watch -n 1 curl -s http://localhost:8080/status
        ```

4.  **Stop the Application**:
    Once you are finished, you can stop and remove the containers, networks, and volumes with:

    ```bash
    docker compose down -v
    ```